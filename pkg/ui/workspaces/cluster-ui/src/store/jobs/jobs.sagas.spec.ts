// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

import { expectSaga } from "redux-saga-test-plan";
import {
  EffectProviders,
  StaticProvider,
  throwError,
} from "redux-saga-test-plan/providers";
import * as matchers from "redux-saga-test-plan/matchers";
import { cockroach } from "@cockroachlabs/crdb-protobuf-client";

import { getJobs } from "src/api/jobsApi";
import {
  refreshJobsSaga,
  requestJobsSaga,
  receivedJobsSaga,
} from "./jobs.sagas";
import { actions, reducer, JobsState } from "./jobs.reducer";
import {
  allJobsFixture,
  earliestRetainedTime,
} from "../../jobs/jobsPage/jobsPage.fixture";

describe("jobs sagas", () => {
  const payload = new cockroach.server.serverpb.JobsRequest({
    limit: 0,
    type: 0,
    status: "",
  });
  const jobsResponse = new cockroach.server.serverpb.JobsResponse({
    jobs: allJobsFixture,
    earliest_retained_time: earliestRetainedTime,
  });

  const jobsAPIProvider: (EffectProviders | StaticProvider)[] = [
    [matchers.call.fn(getJobs), jobsResponse],
  ];

  describe("refreshJobsSaga", () => {
    it("dispatches refresh jobs action", () => {
      return expectSaga(refreshJobsSaga, actions.request(payload))
        .provide(jobsAPIProvider)
        .put(actions.request(payload))
        .run();
    });
  });

  describe("requestJobsSaga", () => {
    it("successfully requests jobs", () => {
      return expectSaga(requestJobsSaga, actions.request(payload))
        .provide(jobsAPIProvider)
        .put(actions.received(jobsResponse))
        .withReducer(reducer)
        .hasFinalState<JobsState>({
          data: jobsResponse,
          lastError: null,
          valid: true,
          inFlight: false,
        })
        .run();
    });

    it("returns error on failed request", () => {
      const error = new Error("Failed request");
      return expectSaga(requestJobsSaga, actions.request(payload))
        .provide([[matchers.call.fn(getJobs), throwError(error)]])
        .put(actions.failed(error))
        .withReducer(reducer)
        .hasFinalState<JobsState>({
          data: null,
          lastError: error,
          valid: false,
          inFlight: false,
        })
        .run();
    });
  });

  describe("receivedJobsSaga", () => {
    it("sets valid status to false after specified period of time", () => {
      const timeout = 500;
      return expectSaga(receivedJobsSaga, timeout)
        .delay(timeout)
        .put(actions.invalidated())
        .withReducer(reducer, {
          data: jobsResponse,
          lastError: null,
          valid: true,
          inFlight: false,
        })
        .hasFinalState<JobsState>({
          data: jobsResponse,
          lastError: null,
          valid: false,
          inFlight: false,
        })
        .run(1000);
    });
  });
});
