// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tree

import (
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/errors"
)

// FunctionName represent a function name in a UDF relevant statement, either
// DDL or DML statement. Similar to TableName, it is constructed for incoming
// SQL queries from an UnresolvedObjectName.
type FunctionName struct {
	objName
}

// MakeFunctionNameFromPrefix returns a FunctionName with the given prefix and
// function name.
func MakeFunctionNameFromPrefix(prefix ObjectNamePrefix, object Name) FunctionName {
	return FunctionName{objName{
		ObjectName:       object,
		ObjectNamePrefix: prefix,
	}}
}

// Format implements the NodeFormatter interface.
func (f *FunctionName) Format(ctx *FmtCtx) {
	f.ObjectNamePrefix.Format(ctx)
	if f.ExplicitSchema || ctx.alwaysFormatTablePrefix() {
		ctx.WriteByte('.')
	}
	ctx.FormatNode(&f.ObjectName)
}

func (f *FunctionName) String() string { return AsString(f) }

// CreateFunction represents a CREATE FUNCTION statement.
type CreateFunction struct {
	IsProcedure bool
	Replace     bool
	FuncName    FunctionName
	Args        FuncArgs
	ReturnType  FuncReturnType
	Options     FunctionOptions
	RoutineBody *RoutineBody
}

// Format implements the NodeFormatter interface.
func (node *CreateFunction) Format(ctx *FmtCtx) {
	ctx.WriteString("CREATE ")
	if node.Replace {
		ctx.WriteString("OR REPLACE ")
	}
	ctx.WriteString("FUNCTION ")
	ctx.FormatNode(&node.FuncName)
	ctx.WriteString("(")
	ctx.FormatNode(node.Args)
	ctx.WriteString(") ")
	ctx.WriteString("RETURNS ")
	if node.ReturnType.IsSet {
		ctx.WriteString("SETOF ")
	}
	ctx.WriteString(node.ReturnType.Type.SQLString())
	ctx.WriteString(" ")
	var funcBody FunctionBodyStr
	for _, option := range node.Options {
		switch t := option.(type) {
		case FunctionBodyStr:
			funcBody = t
			continue
		}
		ctx.FormatNode(option)
		ctx.WriteString(" ")
	}
	if len(funcBody) > 0 {
		ctx.FormatNode(funcBody)
	}
	if node.RoutineBody != nil {
		ctx.WriteString("BEGIN ATOMIC ")
		for _, stmt := range node.RoutineBody.Stmts {
			ctx.FormatNode(stmt)
			ctx.WriteString("; ")
		}
		ctx.WriteString("END")
	}
}

// RoutineBody represent a list of statements in a UDF body.
type RoutineBody struct {
	Stmts Statements
}

// RoutineReturn represent a RETURN statement in a UDF body.
type RoutineReturn struct {
	ReturnVal Expr
}

// Format implements the NodeFormatter interface.
func (node *RoutineReturn) Format(ctx *FmtCtx) {
	ctx.WriteString("RETURN ")
	ctx.FormatNode(node.ReturnVal)
}

// FunctionOptions represent a list of function options.
type FunctionOptions []FunctionOption

// FunctionOption is an interface representing UDF properties.
type FunctionOption interface {
	functionOption()
	NodeFormatter
}

func (FunctionNullInputBehavior) functionOption() {}
func (FunctionVolatility) functionOption()        {}
func (FunctionLeakproof) functionOption()         {}
func (FunctionBodyStr) functionOption()           {}
func (FunctionLanguage) functionOption()          {}

// FunctionNullInputBehavior represent the UDF property on null parameters.
type FunctionNullInputBehavior int

const (
	// FunctionCalledOnNullInput indicates that the function will be given the
	// chance to execute when presented with NULL input. This is the default if
	// no null input behavior is specified.
	FunctionCalledOnNullInput FunctionNullInputBehavior = iota
	// FunctionReturnsNullOnNullInput indicates that the function will result in
	// NULL given any NULL parameter.
	FunctionReturnsNullOnNullInput
	// FunctionStrict is the same as FunctionReturnsNullOnNullInput
	FunctionStrict
)

// Format implements the NodeFormatter interface.
func (node FunctionNullInputBehavior) Format(ctx *FmtCtx) {
	switch node {
	case FunctionCalledOnNullInput:
		ctx.WriteString("CALLED ON NULL INPUT")
	case FunctionReturnsNullOnNullInput:
		ctx.WriteString("RETURNS NULL ON NULL INPUT")
	case FunctionStrict:
		ctx.WriteString("STRICT")
	default:
		panic(pgerror.New(pgcode.InvalidParameterValue, "Unknown function option"))
	}
}

// FunctionVolatility represent UDF volatility property.
type FunctionVolatility int

const (
	// FunctionVolatile represents volatility.Volatile. This is the default
	// volatility if none is provided.
	FunctionVolatile FunctionVolatility = iota
	// FunctionImmutable represents volatility.Immutable.
	FunctionImmutable
	// FunctionStable represents volatility.Stable.
	FunctionStable
)

// Format implements the NodeFormatter interface.
func (node FunctionVolatility) Format(ctx *FmtCtx) {
	switch node {
	case FunctionVolatile:
		ctx.WriteString("VOLATILE")
	case FunctionImmutable:
		ctx.WriteString("IMMUTABLE")
	case FunctionStable:
		ctx.WriteString("STABLE")
	default:
		panic(pgerror.New(pgcode.InvalidParameterValue, "Unknown function option"))
	}
}

// FunctionLeakproof indicates whether if a UDF is leakproof or not. The default
// is NOT LEAKPROOF if no leakproof option is provided. LEAKPROOF can only be
// used with the IMMUTABLE volatility because we currently conflated LEAKPROOF
// as a volatility equal to IMMUTABLE+LEAKPROOF. Postgres allows
// STABLE+LEAKPROOF functions.
type FunctionLeakproof bool

// Format implements the NodeFormatter interface.
func (node FunctionLeakproof) Format(ctx *FmtCtx) {
	if !node {
		ctx.WriteString("NOT ")
	}
	ctx.WriteString("LEAKPROOF")
}

// FunctionLanguage indicates the language of the statements in the UDF function
// body.
type FunctionLanguage int

const (
	_ FunctionLanguage = iota
	// FunctionLangSQL represent SQL language.
	FunctionLangSQL
)

// Format implements the NodeFormatter interface.
func (node FunctionLanguage) Format(ctx *FmtCtx) {
	ctx.WriteString("LANGUAGE ")
	switch node {
	case FunctionLangSQL:
		ctx.WriteString("SQL")
	default:
		panic(pgerror.New(pgcode.InvalidParameterValue, "Unknown function option"))
	}
}

// AsFunctionLanguage converts a string to a FunctionLanguage if applicable.
// Error is returned if string does not represent a valid UDF language.
func AsFunctionLanguage(lang string) (FunctionLanguage, error) {
	switch strings.ToLower(lang) {
	case "sql":
		return FunctionLangSQL, nil
	}
	return 0, errors.Newf("language %q does not exist", lang)
}

// FunctionBodyStr is a string containing all statements in a UDF body.
type FunctionBodyStr string

// Format implements the NodeFormatter interface.
func (node FunctionBodyStr) Format(ctx *FmtCtx) {
	ctx.WriteString("AS ")
	ctx.WriteString("$$")
	ctx.WriteString(string(node))
	ctx.WriteString("$$")
}

// FuncArgs represents a list of FuncArg.
type FuncArgs []FuncArg

// Format implements the NodeFormatter interface.
func (node FuncArgs) Format(ctx *FmtCtx) {
	for i, arg := range node {
		if i > 0 {
			ctx.WriteString(", ")
		}
		ctx.FormatNode(&arg)
	}
}

// FuncArg represents an argument from a UDF signature.
type FuncArg struct {
	Name       Name
	Type       ResolvableTypeReference
	Class      FuncArgClass
	DefaultVal Expr
}

// Format implements the NodeFormatter interface.
func (node *FuncArg) Format(ctx *FmtCtx) {
	switch node.Class {
	case FunctionArgIn:
		ctx.WriteString("IN")
	case FunctionArgOut:
		ctx.WriteString("OUT")
	case FunctionArgInOut:
		ctx.WriteString("INOUT")
	case FunctionArgVariadic:
		ctx.WriteString("VARIADIC")
	default:
		panic(pgerror.New(pgcode.InvalidParameterValue, "Unknown function option"))
	}
	ctx.WriteString(" ")
	if node.Name != "" {
		ctx.FormatNode(&node.Name)
		ctx.WriteString(" ")
	}
	ctx.WriteString(node.Type.SQLString())
	if node.DefaultVal != nil {
		ctx.WriteString(" DEFAULT ")
		ctx.FormatNode(node.DefaultVal)
	}
}

// FuncArgClass indicates what type of argument an arg is.
type FuncArgClass int

const (
	// FunctionArgIn args can only be used as input.
	FunctionArgIn FuncArgClass = iota
	// FunctionArgOut args can only be used as output.
	FunctionArgOut
	// FunctionArgInOut args can be used as both input and output.
	FunctionArgInOut
	// FunctionArgVariadic args are variadic.
	FunctionArgVariadic
)

// FuncReturnType represent the return type of UDF.
type FuncReturnType struct {
	Type  ResolvableTypeReference
	IsSet bool
}

// DropFunction represents a DROP FUNCTION statement.
type DropFunction struct {
	IfExists     bool
	Functions    FuncObjs
	DropBehavior DropBehavior
}

// Format implements the NodeFormatter interface.
func (node *DropFunction) Format(ctx *FmtCtx) {
	ctx.WriteString("DROP FUNCTION ")
	if node.IfExists {
		ctx.WriteString("IF EXISTS ")
	}
	ctx.FormatNode(node.Functions)
	if node.DropBehavior != DropDefault {
		ctx.WriteString(" ")
		ctx.WriteString(node.DropBehavior.String())
	}
}

// FuncObjs is a slice of FuncObj.
type FuncObjs []FuncObj

// Format implements the NodeFormatter interface.
func (node FuncObjs) Format(ctx *FmtCtx) {
	for i, f := range node {
		if i > 0 {
			ctx.WriteString(" ,")
		}
		ctx.FormatNode(f)
	}
}

// FuncObj represents a function object DROP FUNCTION tries to drop.
type FuncObj struct {
	FuncName FunctionName
	Args     FuncArgs
}

// Format implements the NodeFormatter interface.
func (node FuncObj) Format(ctx *FmtCtx) {
	ctx.FormatNode(&node.FuncName)
	if node.Args != nil {
		ctx.WriteString("(")
		ctx.FormatNode(node.Args)
		ctx.WriteString(")")
	}
}

// AlterFunctionOptions represents a ALTER FUNCTION...action statement.
type AlterFunctionOptions struct {
	Function FuncObj
	Options  FunctionOptions
}

// Format implements the NodeFormatter interface.
func (node *AlterFunctionOptions) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER FUNCTION ")
	ctx.FormatNode(node.Function)
	for _, option := range node.Options {
		ctx.WriteString(" ")
		ctx.FormatNode(option)
	}
}

// AlterFunctionRename represents a ALTER FUNCTION...RENAME statement.
type AlterFunctionRename struct {
	Function FuncObj
	NewName  Name
}

// Format implements the NodeFormatter interface.
func (node *AlterFunctionRename) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER FUNCTION ")
	ctx.FormatNode(node.Function)
	ctx.WriteString(" RENAME TO ")
	ctx.WriteString(string(node.NewName))
}

// AlterFunctionSetSchema represents a ALTER FUNCTION...SET SCHEMA statement.
type AlterFunctionSetSchema struct {
	Function      FuncObj
	NewSchemaName Name
}

// Format implements the NodeFormatter interface.
func (node *AlterFunctionSetSchema) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER FUNCTION ")
	ctx.FormatNode(node.Function)
	ctx.WriteString(" SET SCHEMA ")
	ctx.WriteString(string(node.NewSchemaName))
}

// AlterFunctionSetOwner represents the ALTER FUNCTION...OWNER TO statement.
type AlterFunctionSetOwner struct {
	Function FuncObj
	NewOwner RoleSpec
}

// Format implements the NodeFormatter interface.
func (node *AlterFunctionSetOwner) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER FUNCTION ")
	ctx.FormatNode(node.Function)
	ctx.WriteString(" OWNER TO ")
	ctx.FormatNode(&node.NewOwner)
}

// AlterFunctionDepExtension represents the ALTER FUNCTION...DEPENDS ON statement.
type AlterFunctionDepExtension struct {
	Function  FuncObj
	Remove    bool
	Extension Name
}

// Format implements the NodeFormatter interface.
func (node *AlterFunctionDepExtension) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER FUNCTION  ")
	ctx.FormatNode(node.Function)
	if node.Remove {
		ctx.WriteString(" NO")
	}
	ctx.WriteString(" DEPENDS ON EXTENSION ")
	ctx.WriteString(string(node.Extension))
}
