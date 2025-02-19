// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package insights

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/stretchr/testify/require"
)

func TestAnyDetector(t *testing.T) {
	t.Run("enabled is false without any detectors", func(t *testing.T) {
		detector := &anyDetector{}
		require.False(t, detector.enabled())
	})

	t.Run("enabled is false with all disabled detectors", func(t *testing.T) {
		detector := &anyDetector{[]detector{&fakeDetector{}, &fakeDetector{}}}
		require.False(t, detector.enabled())
	})

	t.Run("enabled is true with at least one enabled detector", func(t *testing.T) {
		detector := &anyDetector{[]detector{&fakeDetector{stubEnabled: true}, &fakeDetector{}}}
		require.True(t, detector.enabled())
	})

	t.Run("isOutlier is false without any detectors", func(t *testing.T) {
		detector := &anyDetector{}
		require.False(t, detector.isOutlier(&Statement{}))
	})

	t.Run("isOutlier is false without any concerned detectors", func(t *testing.T) {
		detector := &anyDetector{[]detector{&fakeDetector{}, &fakeDetector{}}}
		require.False(t, detector.isOutlier(&Statement{}))
	})

	t.Run("isOutlier is true with at least one concerned detector", func(t *testing.T) {
		detector := &anyDetector{[]detector{&fakeDetector{stubIsOutlier: true}, &fakeDetector{}}}
		require.True(t, detector.isOutlier(&Statement{}))
	})

	t.Run("isOutlier consults all detectors without short-circuiting", func(t *testing.T) {
		// Detector implementations may wish to observe all statements, to
		// build up their baseline sense of what "usual" is. To short-circuit
		// would deny them that chance.
		d1 := &fakeDetector{stubIsOutlier: true}
		d2 := &fakeDetector{stubIsOutlier: true}
		detector := &anyDetector{[]detector{d1, d2}}
		detector.isOutlier(&Statement{})
		require.True(t, d1.isOutlierCalled, "the first detector should be consulted")
		require.True(t, d2.isOutlierCalled, "the second detector should be consulted")
	})
}

type IsOutlierTestCase struct {
	name             string
	seedLatency      time.Duration
	candidateLatency time.Duration
	isOutlier        bool
}

type MetricsTestCase struct {
	name         string
	fingerprints int
	assertion    func(*testing.T, Metrics)
}

func TestLatencyQuantileDetector(t *testing.T) {
	t.Run("enabled false by default", func(t *testing.T) {
		d := newLatencyQuantileDetector(cluster.MakeTestingClusterSettings(), NewMetrics())
		require.False(t, d.enabled())
	})

	t.Run("enabled true by cluster setting", func(t *testing.T) {
		st := cluster.MakeTestingClusterSettings()
		d := newLatencyQuantileDetector(st, NewMetrics())
		LatencyQuantileDetectorEnabled.Override(context.Background(), &st.SV, true)
		require.True(t, d.enabled())
	})

	t.Run("isOutlier", func(t *testing.T) {
		ctx := context.Background()
		st := cluster.MakeTestingClusterSettings()
		LatencyQuantileDetectorEnabled.Override(ctx, &st.SV, true)
		LatencyQuantileDetectorInterestingThreshold.Override(ctx, &st.SV, 100*time.Millisecond)

		tests := []IsOutlierTestCase{{
			name:             "false with normal latency",
			seedLatency:      100 * time.Millisecond,
			candidateLatency: 100 * time.Millisecond,
			isOutlier:        false,
		}, {
			name:             "true with higher latency",
			seedLatency:      100 * time.Millisecond,
			candidateLatency: 200 * time.Millisecond,
			isOutlier:        true,
		}, {
			name:             "false with higher latency under interesting threshold",
			seedLatency:      10 * time.Millisecond,
			candidateLatency: 20 * time.Millisecond,
			isOutlier:        false,
		}}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				d := newLatencyQuantileDetector(st, NewMetrics())
				for i := 0; i < 1000; i++ {
					d.isOutlier(&Statement{LatencyInSeconds: test.seedLatency.Seconds()})
				}
				require.Equal(t, test.isOutlier, d.isOutlier(&Statement{LatencyInSeconds: test.candidateLatency.Seconds()}))
			})
		}
	})

	t.Run("metrics", func(t *testing.T) {
		ctx := context.Background()
		st := cluster.MakeTestingClusterSettings()
		LatencyQuantileDetectorEnabled.Override(ctx, &st.SV, true)
		LatencyQuantileDetectorMemoryCap.Override(ctx, &st.SV, 1024)

		tests := []MetricsTestCase{{
			name:         "reports distinct fingerprints",
			fingerprints: 1,
			assertion: func(t *testing.T, metrics Metrics) {
				require.Equal(t, int64(1), metrics.Fingerprints.Value())
			},
		}, {
			// Each Stream with one observation requires ~104 bytes,
			// so the 20 Streams here will consume ~2 kilobytes of memory,
			// surpassing the above 1 kilobyte cap and triggering some evictions.
			// We don't assume a precise number of evictions because platform
			// differences may lead to different memory usage calculations.
			name:         "reports distinct fingerprints, taking eviction into account",
			fingerprints: 20,
			assertion: func(t *testing.T, metrics Metrics) {
				require.Less(t, metrics.Fingerprints.Value(), int64(20))
			},
		}, {
			name:         "reports memory usage",
			fingerprints: 1,
			assertion: func(t *testing.T, metrics Metrics) {
				require.Greater(t, metrics.Memory.Value(), int64(0))
			},
		}, {
			name:         "reports memory usage, taking eviction into account",
			fingerprints: 20,
			assertion: func(t *testing.T, metrics Metrics) {
				require.LessOrEqual(t, metrics.Memory.Value(), int64(1024))
			},
		}, {
			name:         "reports evictions",
			fingerprints: 20,
			assertion: func(t *testing.T, metrics Metrics) {
				require.Greater(t, metrics.Evictions.Count(), int64(0))
			},
		}}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				metrics := NewMetrics()
				d := newLatencyQuantileDetector(st, metrics)
				// Show the detector `test.fingerprints` distinct fingerprints.
				for i := 0; i < test.fingerprints; i++ {
					d.isOutlier(&Statement{
						LatencyInSeconds: LatencyQuantileDetectorInterestingThreshold.Get(&st.SV).Seconds(),
						FingerprintID:    roachpb.StmtFingerprintID(i),
					})
				}
				test.assertion(t, metrics)
			})
		}
	})
}

// dev bench pkg/sql/sqlstats/outliers  --bench-mem --verbose
// BenchmarkLatencyQuantileDetector-16    	 1589583	       701.1 ns/op	      24 B/op	       1 allocs/op
func BenchmarkLatencyQuantileDetector(b *testing.B) {
	random := rand.New(rand.NewSource(42))
	settings := cluster.MakeTestingClusterSettings()
	LatencyQuantileDetectorEnabled.Override(context.Background(), &settings.SV, true)
	d := newLatencyQuantileDetector(settings, NewMetrics())
	for i := 0; i < b.N; i++ {
		d.isOutlier(&Statement{
			LatencyInSeconds: random.Float64(),
		})
	}
}

func TestLatencyThresholdDetector(t *testing.T) {
	t.Run("enabled false by default", func(t *testing.T) {
		detector := latencyThresholdDetector{st: cluster.MakeTestingClusterSettings()}
		require.False(t, detector.enabled())
	})

	t.Run("enabled true with nonzero threshold", func(t *testing.T) {
		st := cluster.MakeTestingClusterSettings()
		LatencyThreshold.Override(context.Background(), &st.SV, 1*time.Second)
		detector := latencyThresholdDetector{st: st}
		require.True(t, detector.enabled())
	})

	t.Run("isOutlier false when disabled", func(t *testing.T) {
		detector := latencyThresholdDetector{st: cluster.MakeTestingClusterSettings()}
		require.False(t, detector.isOutlier(&Statement{LatencyInSeconds: 1}))
	})

	t.Run("isOutlier false when fast enough", func(t *testing.T) {
		st := cluster.MakeTestingClusterSettings()
		LatencyThreshold.Override(context.Background(), &st.SV, 1*time.Second)
		detector := latencyThresholdDetector{st: st}
		require.False(t, detector.isOutlier(&Statement{LatencyInSeconds: 0.5}))
	})

	t.Run("isOutlier true beyond threshold", func(t *testing.T) {
		st := cluster.MakeTestingClusterSettings()
		LatencyThreshold.Override(context.Background(), &st.SV, 1*time.Second)
		detector := latencyThresholdDetector{st: st}
		require.True(t, detector.isOutlier(&Statement{LatencyInSeconds: 1}))
	})
}

type fakeDetector struct {
	stubEnabled     bool
	stubIsOutlier   bool
	isOutlierCalled bool
}

func (f fakeDetector) enabled() bool {
	return f.stubEnabled
}

func (f *fakeDetector) isOutlier(_ *Statement) bool {
	f.isOutlierCalled = true
	return f.stubIsOutlier
}

var _ detector = &fakeDetector{}
