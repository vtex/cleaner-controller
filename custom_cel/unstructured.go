package custom_cel

// todo: add golang docs
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

func Unstructured() cel.EnvOption {
	return cel.Lib(unstructuredLib{})
}

type unstructuredLib struct{}

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
