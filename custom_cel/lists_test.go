package custom_cel

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
	"time"
)

var varName = "objects"

func Test_sort(t *testing.T) {
	first, second, third := getDates()

	testCases := map[string]struct {
		condition string
		list      any
		wantList  ref.Val
	}{
		"sort timestamp list": {
			condition: `objects.sort_by(i, i)`,
			list:      []time.Time{second, first, third},
			wantList: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: first},
					{Time: second},
					{Time: third},
				}),
		},

		"sort duration list": {
			condition: `objects.sort_by(i, i)`,
			list: []time.Duration{
				time.Duration(second.Unix()),
				time.Duration(first.Unix()),
				time.Duration(third.Unix()),
			},
			wantList: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Duration{
					{Duration: time.Duration(first.Unix())},
					{Duration: time.Duration(second.Unix())},
					{Duration: time.Duration(third.Unix())},
				}),
		},

		"sort int list": {
			condition: `[2,1,3].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Int{1, 2, 3}),
		},

		"sort uint list": {
			condition: `[uint(2), uint(1), uint(3)].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Uint{1, 2, 3}),
		},

		"sort double list": {
			condition: `[double(2), double(1), double(3)].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Double{1, 2, 3}),
		},

		"sort bytes list": {
			condition: `[bytes("c"), bytes("a"), bytes("b")].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Bytes{[]byte("a"), []byte("b"), []byte("c")}),
		},

		"sort boolean list": {
			condition: `[true, false, true].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Bool{false, true, true}),
		},

		"sort string list": {
			condition: `["c", "a", "b"].sort_by(i,i)`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.String{"a", "b", "c"}),
		},

		"sort unstructured list by timestamp": {
			condition: `objects.items.sort_by(o, o.metadata.creationTimestamp)`,
			list:      generateUnorderedUl(t, first.Format(time.RFC3339Nano), second.Format(time.RFC3339Nano), third.Format(time.RFC3339Nano)),
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, generateOrderedSlice(t, first.Format(time.RFC3339Nano), second.Format(time.RFC3339Nano), third.Format(time.RFC3339Nano))),
		},
	}

	evaluateTestCases(t, testCases)
}

func Test_reverse(t *testing.T) {
	first, second, third := getDates()

	testCases := map[string]struct {
		condition string
		list      any
		wantList  ref.Val
	}{
		"reverse timestamp list": {
			condition: `objects.reverse_list()`,
			list:      []time.Time{first, second, third},
			wantList: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: third},
					{Time: second},
					{Time: first},
				}),
		},

		"reverse duration list": {
			condition: `objects.reverse_list()`,
			list: []time.Duration{
				time.Duration(first.Unix()),
				time.Duration(second.Unix()),
				time.Duration(third.Unix()),
			},
			wantList: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Duration{
					{Duration: time.Duration(third.Unix())},
					{Duration: time.Duration(second.Unix())},
					{Duration: time.Duration(first.Unix())},
				}),
		},

		"reverse int list": {
			condition: `[3,2,1].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Int{1, 2, 3}),
		},

		"reverse uint list": {
			condition: `[uint(3), uint(2), uint(1)].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Uint{1, 2, 3}),
		},

		"reverse double list": {
			condition: `[double(3), double(2), double(1)].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Double{1, 2, 3}),
		},

		"reverse bytes list": {
			condition: `[bytes("c"), bytes("b"), bytes("a")].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Bytes{[]byte("a"), []byte("b"), []byte("c")}),
		},

		"reverse boolean list": {
			condition: `[true, true, false].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.Bool{false, true, true}),
		},

		"reverse string list": {
			condition: `["c", "b", "a"].reverse_list()`,
			wantList:  types.NewDynamicList(types.DefaultTypeAdapter, []types.String{"a", "b", "c"}),
		},
	}

	evaluateTestCases(t, testCases)
}

func evaluateTestCases(t *testing.T, testCases map[string]struct {
	condition string
	list      any
	wantList  ref.Val
}) {
	for description, tc := range testCases {
		t.Run(description, func(t *testing.T) {
			prg := setupProgram(t, varName, tc.condition)

			gotList, _, gotErr := prg.Eval(map[string]interface{}{
				varName: tc.list,
			})

			if gotErr != nil {
				t.Fatalf("eval error: %s", gotErr)
			}

			if gotList.Equal(tc.wantList) != types.True {
				t.Errorf("\ngot=%v\nwant=%v", gotList, tc.wantList)
			}
		})
	}
}

func setupProgram(t *testing.T, varName string, condition string) cel.Program {
	env, err := cel.NewEnv(
		cel.Variable(varName, cel.DynType),
		Lists(),
	)
	if err != nil {
		t.Fatalf("unable to create new env: %s", err)
	}

	ast, issues := env.Compile(condition)
	if issues != nil && issues.Err() != nil {
		t.Fatalf("compile error: %s", issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program error: %s", err)
	}

	return prg
}

func generateUnorderedUl(t *testing.T, first, second, third string) map[string]interface{} {
	t.Helper()
	items := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"creationTimestamp": second,
				},
			},
		},

		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"creationTimestamp": third,
				},
			},
		},

		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"creationTimestamp": first,
				},
			},
		},
	}

	ul := &unstructured.UnstructuredList{
		Items: items,
	}

	return ul.UnstructuredContent()
}

func generateOrderedSlice(t *testing.T, first, second, third string) []map[string]interface{} {
	t.Helper()
	orderedItems := make([]map[string]interface{}, 0)
	orderedItems = append(orderedItems,
		map[string]interface{}{
			"metadata": map[string]interface{}{
				"creationTimestamp": first,
			},
		},

		map[string]interface{}{
			"metadata": map[string]interface{}{
				"creationTimestamp": second,
			},
		},

		map[string]interface{}{
			"metadata": map[string]interface{}{
				"creationTimestamp": third,
			},
		},
	)
	return orderedItems
}

func getDates() (time.Time, time.Time, time.Time) {
	now := time.Now()
	first := now.Add(-(time.Duration(24) * time.Hour * 3))
	second := now.Add(-(time.Duration(24) * time.Hour * 2))
	third := now.Add(-(time.Duration(24) * time.Hour * 1))
	return first, second, third
}
