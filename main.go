package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/parser"
	"sigs.k8s.io/yaml"
)

const (
	AscendingOrder  = "asc"
	DescendingOrder = "desc"
)

func extractIdent(e ast.Expr) (string, bool) {
	switch e.Kind() {
	case ast.IdentKind:
		return e.AsIdent(), true
	}
	return "", false
}

func MakeSortBy(eh parser.ExprHelper, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, found := extractIdent(args[0])
	if !found {
		return nil, eh.NewError(args[0].ID(), "argument is not an identifier")
	}

	var fn ast.Expr = args[1]

	init := eh.NewList()
	condition := eh.NewLiteral(types.True)

	step := eh.NewCall(operators.Add, eh.NewAccuIdent(), eh.NewList(
		eh.NewCall("pair", fn, args[0]),
	))

	/*
	   This comprehension is expanded to:
	   __result__ = [] # init expr
	   for $v in $target:
	       __result__ += [pair(fn(v), v)] # step expr
	   return sort(__result__, "asc") # result expr
	*/
	mapped := eh.NewComprehension(
		target,
		v,
		parser.AccumulatorName,
		init,
		condition,
		step,
		eh.NewCall(
			"sort",
			eh.NewAccuIdent(),
			eh.NewLiteral(types.DefaultTypeAdapter.NativeToValue("asc")),
		),
	)

	return mapped, nil
}

var sortByMacro = parser.NewReceiverMacro("sort_by", 2, MakeSortBy)

func main() {
	if len(os.Args) != 2 {
		panic("usage: cel-test <program-file>")
	}
	expr, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	dynListType := cel.ListType(cel.DynType)
	env, err := cel.NewEnv(
		ext.Strings(),
		cel.Macros(sortByMacro),
		cel.Function(
			"pair",
			cel.Overload(
				"make_pair",
				[]*cel.Type{cel.DynType, cel.DynType},
				cel.DynType,
				cel.BinaryBinding(makePair),
			),
		),
		cel.Function(
			"sort",
			cel.Overload(
				"sort_by_order",
				[]*cel.Type{dynListType, cel.DynType},
				dynListType,
				cel.BinaryBinding(sortByOrder),
			),
		),
		cel.Variable("values", cel.DynType),
	)
	if err != nil {
		panic(err)
	}

	ast, issues := env.Compile(string(expr))
	if issues != nil && issues.Err() != nil {
		panic(issues.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		panic(err)
	}
	celCtxBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	celCtx := make(map[string]interface{})
	if err := yaml.Unmarshal(celCtxBytes, &celCtx); err != nil {
		panic(err)
	}

	out, _, err := prg.Eval(celCtx)
	if err != nil {
		panic(err)
	}
	output, err := yaml.Marshal(out.Value())
	if err != nil {
		panic(err)
	}
	fmt.Print(string(output))
}

var (
	orderKey = types.DefaultTypeAdapter.NativeToValue("order")
	valueKey = types.DefaultTypeAdapter.NativeToValue("value")
)

func makePair(order ref.Val, value ref.Val) ref.Val {
	if _, ok := order.(traits.Comparer); !ok {
		return types.ValOrErr(order, "unable to build ordered pair with value %v", order.Value())
	}
	return types.NewStringInterfaceMap(types.DefaultTypeAdapter, map[string]any{
		"order": order.Value(),
		"value": value.Value(),
	})
}

type pair struct {
	order ref.Val
	value ref.Val
}

func sortByOrder(itemsVal ref.Val, orderVal ref.Val) ref.Val {
	items, ok := itemsVal.(traits.Lister)
	if !ok {
		return types.ValOrErr(itemsVal, "unable to convert to traits.Lister")
	}

	order, ok := orderVal.Value().(string)
	if !ok {
		return types.ValOrErr(orderVal, "unable to convert to ref.Val string")
	}

	pairs := make([]pair, 0, items.Size().Value().(int64))
	for it := items.Iterator(); it.HasNext().(types.Bool); {
		curr := it.Next().(traits.Mapper) // handle cast error

		pairs = append(pairs, pair{
			order: curr.Get(orderKey),
			value: curr.Get(valueKey),
		})
	}

	ascSort := func(i, j int) bool {
		cmp := pairs[i].order.(traits.Comparer)
		switch cmp.Compare(pairs[j].order) {
		case types.IntNegOne:
			return true
		case types.IntOne:
			return false
		default: // IntZero means equal
			return false
		}
	}

	descSort := func(i, j int) bool {
		cmp := pairs[i].order.(traits.Comparer)
		switch cmp.Compare(pairs[j].order) {
		case types.IntNegOne:
			return false
		case types.IntOne:
			return true
		default: // IntZero means equal
			return false
		}
	}

	switch strings.ToLower(order) {
	case AscendingOrder:
		sort.Slice(pairs, ascSort)
	case DescendingOrder:
		sort.Slice(pairs, descSort)
	default:
		return types.NewErr("unknown order: %s", order)
	}

	var ordered []interface{}
	for _, v := range pairs {
		ordered = append(ordered, v.value.Value())
	}

	return types.NewDynamicList(types.DefaultTypeAdapter, ordered)
}
