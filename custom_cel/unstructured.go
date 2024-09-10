package custom_cel

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apiserver/pkg/cel/library"
	"sort"
	"strings"
)

const (
	AscendingOrder  = "asc"
	DescendingOrder = "desc"
)

// Unstructured returns a cel.EnvOption to configure extended functions for Unstructured manipulation.
// # SortUnstructured
//
// Returns a new list ordered by the creation timestamp field.
//
// sort(<list>, "order") -> <list>
//
// Examples:
//
// Input 1:
//
// sort({[
//
//	{map[metadata:map[creationTimestamp:2024-09-08T09:17:17.527033-03:00]]},
//	{map[metadata:map[creationTimestamp:2024-09-09T09:17:17.527033-03:00]]},
//
// ], "desc"}
//
// Output 1:
//
// {[
//
//	{map[metadata:map[creationTimestamp:2024-09-09T09:17:17.527033-03:00]]},
//	{map[metadata:map[creationTimestamp:2024-09-08T09:17:17.527033-03:00]]},
//
// ]}
//
// Input 2:
//
// sort({[
//
//	{map[metadata:map[creationTimestamp:2024-09-09T09:17:17.527033-03:00]]},
//	{map[metadata:map[creationTimestamp:2024-09-08T09:17:17.527033-03:00]]},
//
// ], "asc"}
//
// Output 2:
//
// {[
//
//	{map[metadata:map[creationTimestamp:2024-09-08T09:17:17.527033-03:00]]},
//	{map[metadata:map[creationTimestamp:2024-09-09T09:17:17.527033-03:00]]},
//
// ]}
//
// Input 3:
//
// sort({[
//
//	{map[metadata:map[creationTimestamp:2024-09-09T09:17:17.527033-03:00]]},
//	{map[metadata:map[creationTimestamp:2024-09-08T09:17:17.527033-03:00]]},
//
// ], "unknown-order"}
//
// Output 3:
// error
func Unstructured() cel.EnvOption {
	return cel.Lib(unstructuredLib{})
}

type unstructuredLib struct{}

// CompileOptions implements the Library interface method defining the basic compile configuration
func (u unstructuredLib) CompileOptions() []cel.EnvOption {
	dynListType := cel.ListType(cel.DynType)
	return []cel.EnvOption{
		library.Lists(),
		cel.Function(
			"sort",
			cel.Overload(
				"sort_unstructured",
				[]*cel.Type{dynListType, cel.StringType},
				dynListType,
				cel.BinaryBinding(sortUnstructured),
			),
		),
	}
}

// ProgramOptions implements the Library interface method defining the basic program options
func (u unstructuredLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{}
}

func sortUnstructured(itemsVal, orderVal ref.Val) ref.Val {
	items, ok := itemsVal.(traits.Lister)
	if !ok {
		return types.ValOrErr(itemsVal, "unable to convert to traits.Lister")
	}

	orderedItems := make([]map[string]interface{}, 0)

	for it := items.Iterator(); it.HasNext().(types.Bool); {
		currItem := it.Next().Value()
		item := currItem.(map[string]interface{})
		orderedItems = append(orderedItems, item)
	}

	ascSort := func(i, j int) bool {
		left, right := getContentToCompare(orderedItems, i, j)
		return left.GetCreationTimestamp().Time.
			Before(right.GetCreationTimestamp().Time)
	}

	descSort := func(i, j int) bool {
		left, right := getContentToCompare(orderedItems, i, j)
		return left.GetCreationTimestamp().Time.
			After(right.GetCreationTimestamp().Time)
	}

	order := orderVal.Value().(string)
	switch strings.ToLower(order) {
	case AscendingOrder:
		sort.Slice(orderedItems, ascSort)
	case DescendingOrder:
		sort.Slice(orderedItems, descSort)
	default:
		return types.NewErr("unknown order: %s", order)
	}

	return types.NewDynamicList(types.DefaultTypeAdapter, orderedItems)
}

func getContentToCompare(orderedItems []map[string]interface{}, i int, j int) (*unstructured.Unstructured, *unstructured.Unstructured) {
	left := &unstructured.Unstructured{}
	left.SetUnstructuredContent(orderedItems[i])

	right := &unstructured.Unstructured{}
	right.SetUnstructuredContent(orderedItems[j])

	return left, right
}
