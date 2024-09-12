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
	first := now.Add(-(time.Duration(24) * time.Hour * 3)).Format(time.RFC3339Nano)
	second := now.Add(-(time.Duration(24) * time.Hour * 2)).Format(time.RFC3339Nano)
	third := now.Add(-(time.Duration(24) * time.Hour * 1)).Format(time.RFC3339Nano)

	testCases := map[string]struct {
		condition string
		ul        map[string]interface{}
		wantUlDyn ref.Val
	}{
		"sort unstructured in ascending order by default": {
			condition: `objects.items.sort_by(v, v.metadata.creationTimestamp)`,
			ul:        generateUnorderedUl(t, first, second, third),
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, generateOrderedSlice(t, first, second, third)),
		},

		"sort unstructured in descending order": {
			condition: `objects.items.sort_by(v, v.metadata.creationTimestamp, "desc")`,
			ul:        generateUnorderedUl(t, first, second, third),
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, generateOrderedSlice(t, third, second, first)),
		},

		"sort unstructured in ascending order": {
			condition: `objects.items.sort_by(v, v.metadata.creationTimestamp, "asc")`,
			ul:        generateUnorderedUl(t, first, second, third),
			wantUlDyn: types.NewDynamicList(types.DefaultTypeAdapter, generateOrderedSlice(t, first, second, third)),
		},
	}

	for description, tc := range testCases {
		t.Run(description, func(t *testing.T) {
			prg := setupProgram(t, varName, tc.condition)

			gotUlDyn, _, gotErr := prg.Eval(map[string]interface{}{
				varName: tc.ul,
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
