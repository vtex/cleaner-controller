package custom_cel

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/parser"
	"k8s.io/apiserver/pkg/cel/library"
	"sort"
)

// Lists returns a cel.EnvOption to configure extended functions Lists manipulation.
//
// # SortBy
//
// Returns a new sorted list by the field defined.
// It supports all types that implements the base traits.Comparer interface.
//
// <list>.sort_by(obj, obj.field) ==> <list>
//
// Examples:
//
// [2,3,1].sort_by(i,i) ==> [1,2,3]
//
// [{Name: "c", Age: 10}, {Name: "a", Age: 30}, {Name: "b", Age: 1}].sort_by(obj, obj.age) ==> [{Name: "b", Age: 1}, {Name: "c", Age: 10}, {Name: "a", Age: 30}]
//
// # ReverseList
//
// Returns a new list in reverse order.
// It supports all types that implements the base traits.Comparer interface
//
// <list>.reverse_list() ==> <list>
//
// # Examples
//
// [1,2,3].reverse_list() ==> [3,2,1]
//
// ["x", "y", "z"].reverse_list() ==> ["z", "y", "x"]
func Lists() cel.EnvOption {
	return cel.Lib(listsLib{})
}

type listsLib struct{}

// CompileOptions implements the Library interface method defining the basic compile configuration
func (u listsLib) CompileOptions() []cel.EnvOption {
	dynListType := cel.ListType(cel.DynType)
	sortByMacro := parser.NewReceiverMacro("sort_by", 2, makeSortBy)
	return []cel.EnvOption{
		library.Lists(),
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
				"sort_list",
				[]*cel.Type{dynListType},
				dynListType,
				cel.UnaryBinding(makeSort),
			),
		),
		cel.Function(
			"reverse_list",
			cel.MemberOverload(
				"reverse_list_id",
				[]*cel.Type{cel.ListType(cel.DynType)},
				cel.ListType(cel.DynType),
				cel.UnaryBinding(makeReverse),
			),
		),
	}
}

// ProgramOptions implements the Library interface method defining the basic program options
func (u listsLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{}
}

type pair struct {
	order ref.Val
	value ref.Val
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

func makeSort(itemsVal ref.Val) ref.Val {
	items, ok := itemsVal.(traits.Lister)
	if !ok {
		return types.ValOrErr(itemsVal, "unable to convert to traits.Lister")
	}

	pairs := make([]pair, 0, items.Size().Value().(int64))
	index := 0
	for it := items.Iterator(); it.HasNext().(types.Bool); {
		curr, ok := it.Next().(traits.Mapper)
		if !ok {
			return types.NewErr("unable to convert elem %d to traits.Mapper", index)
		}

		pairs = append(pairs, pair{
			order: curr.Get(orderKey),
			value: curr.Get(valueKey),
		})
		index++
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].order.(traits.Comparer).Compare(pairs[j].order) == types.IntNegOne
	})

	var ordered []interface{}
	for _, v := range pairs {
		ordered = append(ordered, v.value.Value())
	}

	return types.NewDynamicList(types.DefaultTypeAdapter, ordered)
}

func extractIdent(e ast.Expr) (string, bool) {
	if e.Kind() == ast.IdentKind {
		return e.AsIdent(), true
	}
	return "", false
}

func makeSortBy(eh parser.ExprHelper, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, found := extractIdent(args[0])
	if !found {
		return nil, eh.NewError(args[0].ID(), "argument is not an identifier")
	}

	var fn = args[1]

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
	   return sort(__result__) # result expr
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
		),
	)

	return mapped, nil
}

func makeReverse(itemsVal ref.Val) ref.Val {
	items, ok := itemsVal.(traits.Lister)
	if !ok {
		return types.ValOrErr(itemsVal, "unable to convert to traits.Lister")
	}

	orderedItems := make([]ref.Val, 0, items.Size().Value().(int64))
	for it := items.Iterator(); it.HasNext().(types.Bool); {
		orderedItems = append(orderedItems, it.Next())
	}

	for i := len(orderedItems)/2 - 1; i >= 0; i-- {
		opp := len(orderedItems) - 1 - i
		orderedItems[i], orderedItems[opp] = orderedItems[opp], orderedItems[i]
	}

	return types.NewDynamicList(types.DefaultTypeAdapter, orderedItems)
}
