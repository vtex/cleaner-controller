package custom_cel

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"k8s.io/apiserver/pkg/cel/library"
	"sort"
	"strings"
)

const (
	AscendingOrder  = "asc"
	DescendingOrder = "desc"
)

// Lists returns a cel.EnvOption to configure extended functions Lists manipulation.
// # Sort
//
// Returns a new sorted list by the order defined (ascending or descending).
// It supports all types that implements the base traits.Comparer interface.
//
// sort(<list>, "order") -> <list>
//
// Examples:
//
// sort(["c", "b", "a"], "asc") // return ["a", "b", "c"]
// sort([2, 1, 3], "desc")        // return [3, 2, 1]
func Lists() cel.EnvOption {
	return cel.Lib(listsLib{})
}

type listsLib struct{}

// CompileOptions implements the Library interface method defining the basic compile configuration
func (u listsLib) CompileOptions() []cel.EnvOption {
	dynListType := cel.ListType(cel.DynType)
	return []cel.EnvOption{
		library.Lists(),
		cel.Function(
			"sort",
			cel.Overload(
				"sort_by_order",
				[]*cel.Type{dynListType, cel.DynType},
				dynListType,
				cel.BinaryBinding(sortByOrder),
			),
		),
	}
}

// ProgramOptions implements the Library interface method defining the basic program options
func (u listsLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{}
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

	itemsSlice := make([]ref.Val, 0)
	for it := items.Iterator(); it.HasNext().(types.Bool); {
		curr := it.Next()
		itemsSlice = append(itemsSlice, curr)
	}

	ascSort := func(i, j int) bool {
		cmp := itemsSlice[i].(traits.Comparer)
		switch cmp.Compare(itemsSlice[j]) {
		case types.IntNegOne:
			return true
		case types.IntOne:
			return false
		default: // IntZero means equal
			return false
		}
	}

	descSort := func(i, j int) bool {
		cmp := itemsSlice[i].(traits.Comparer)
		switch cmp.Compare(itemsSlice[j]) {
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
		sort.Slice(itemsSlice, ascSort)
	case DescendingOrder:
		sort.Slice(itemsSlice, descSort)
	default:
		return types.NewErr("unknown order: %s", order)
	}

	return types.NewDynamicList(types.DefaultTypeAdapter, itemsSlice)
}
