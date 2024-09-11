package custom_cel

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
	"time"
)

func Test_sortByOrder(t *testing.T) {
	varName := "objects"

	now := time.Now()
	first := now.Add(-(time.Duration(24) * time.Hour * 3))
	second := now.Add(-(time.Duration(24) * time.Hour * 2))
	third := now.Add(-(time.Duration(24) * time.Hour * 1))

	testCases := map[string]struct {
		condition string
		list      any
		wantUlDyn ref.Val
	}{
		"sort timestamp list in descending order": {
			condition: `sort(objects, "desc")`,
			list:      []time.Time{second, first, third},
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: third},
					{Time: second},
					{Time: first},
				}),
		},

		"sort timestamp list in ascending order": {
			condition: `sort(objects, "asc")`,
			list:      []time.Time{second, first, third},
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: first},
					{Time: second},
					{Time: third},
				}),
		},

		"sort duration list in descending order": {
			condition: `sort(objects, "desc")`,
			list: []time.Duration{
				time.Duration(second.Unix()),
				time.Duration(first.Unix()),
				time.Duration(third.Unix()),
			},
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Duration{
					{Duration: time.Duration(third.Unix())},
					{Duration: time.Duration(second.Unix())},
					{Duration: time.Duration(first.Unix())},
				}),
		},

		"sort duration list in ascending order": {
			condition: `sort(objects, "asc")`,
			list: []time.Duration{
				time.Duration(second.Unix()),
				time.Duration(first.Unix()),
				time.Duration(third.Unix()),
			},
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Duration{
					{Duration: time.Duration(first.Unix())},
					{Duration: time.Duration(second.Unix())},
					{Duration: time.Duration(third.Unix())},
				}),
		},

		"sort int list in descending order": {
			condition: `sort([2,1,3], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Int{3, 2, 1}),
		},

		"sort int list in ascending order": {
			condition: `sort([2,1,3], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Int{1, 2, 3}),
		},

		"sort uint list in descending order": {
			condition: `sort([uint(2), uint(1), uint(3)], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Uint{3, 2, 1}),
		},

		"sort uint list in ascending order": {
			condition: `sort([uint(2), uint(1), uint(3)], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Uint{1, 2, 3}),
		},

		"sort double list in descending order": {
			condition: `sort([double(2), double(1), double(3)], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Double{3, 2, 1}),
		},

		"sort double list in ascending order": {
			condition: `sort([double(2), double(1), double(3)], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Double{1, 2, 3}),
		},

		"sort bytes list in descending order": {
			condition: `sort([bytes("c"), bytes("a"), bytes("b")], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Bytes{[]byte("c"), []byte("b"), []byte("a")}),
		},

		"sort bytes list in ascending order": {
			condition: `sort([bytes("c"), bytes("a"), bytes("b")], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Bytes{[]byte("a"), []byte("b"), []byte("c")}),
		},

		"sort boolean list in descending order": {
			condition: `sort([true, false, true], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Bool{true, true, false}),
		},

		"sort boolean list in ascending order": {
			condition: `sort([true, false, true], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.Bool{false, true, true}),
		},

		"sort string list in descending order": {
			condition: `sort(["c", "a", "b"], "desc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.String{"c", "b", "a"}),
		},

		"sort string list in ascending order": {
			condition: `sort(["c", "a", "b"], "asc")`,
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, []types.String{"a", "b", "c"}),
		},

		"sort unstructured list in descending order by timestamp": {
			condition: `sort(objects.items.map(item, timestamp(item.metadata.creationTimestamp)), "desc")`,
			list:      generateUnorderedUl(t, first.Format(time.RFC3339Nano), second.Format(time.RFC3339Nano), third.Format(time.RFC3339Nano)),
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: third},
					{Time: second},
					{Time: first},
				},
			),
		},

		"sort unstructured list in ascending order by timestamp": {
			condition: `sort(objects.items.map(item, timestamp(item.metadata.creationTimestamp)), "asc")`,
			list:      generateUnorderedUl(t, first.Format(time.RFC3339Nano), second.Format(time.RFC3339Nano), third.Format(time.RFC3339Nano)),
			wantUlDyn: types.NewDynamicList(
				types.DefaultTypeAdapter,
				[]types.Timestamp{
					{Time: first},
					{Time: second},
					{Time: third},
				},
			),
		},
	}

	for description, tc := range testCases {
		t.Run(description, func(t *testing.T) {
			prg := setupProgram(t, varName, tc.condition)

			gotUlDyn, _, gotErr := prg.Eval(map[string]interface{}{
				varName: tc.list,
			})

			if gotErr != nil {
				t.Fatalf("eval error: %s", gotErr)
			}

			if gotUlDyn.Equal(tc.wantUlDyn) != types.True {
				t.Errorf("\ngot=%v\nwant=%v", gotUlDyn, tc.wantUlDyn)
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
