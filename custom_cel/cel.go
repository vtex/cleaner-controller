package custom_cel

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	cleanerv1alpha1 "github.com/vtex/cleaner-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildCELOptions builds the list of env options to be used when
// building the CEL environment used to evaluated the conditions
// of a given cTTL.
func BuildCELOptions(cTTL *cleanerv1alpha1.ConditionalTTL) []cel.EnvOption {
	r := []cel.EnvOption{
		ext.Strings(), // helper string functions
		Lists(),       // custom VTEX helper for list functions
		cel.Variable("time", cel.TimestampType),
	}
	for _, t := range cTTL.Spec.Targets {
		if t.IncludeWhenEvaluating {
			r = append(r, cel.Variable(t.Name, cel.DynType))
		}
	}
	return r
}

// BuildCELContext builds the map of parameters to be passed to the CEL
// evaluation given a list of TargetStatus and an evaluation time.
func BuildCELContext(targets []cleanerv1alpha1.TargetStatus, time time.Time) map[string]interface{} {
	ctx := make(map[string]interface{})
	for _, ts := range targets {
		if !ts.IncludeWhenEvaluating {
			continue
		}
		ctx[ts.Name] = ts.State.UnstructuredContent()
	}
	ctx["time"] = time
	return ctx
}

// EvaluateCELConditions compiles and evaluates all the conditions on the passed CEL context,
// returning true only when all conditions evaluate to true. It stops evaluating on the first
// encountered error but otherwise all conditions are evaluated in order to find and report
// compilation and/or evaluation errors early. It also updates the passed
// readyCondition Status, Type, Reason and Message fields.
func EvaluateCELConditions(opts []cel.EnvOption, celCtx map[string]interface{}, conditions []string, readyCondition *metav1.Condition) (conditionsMet bool, retryable bool) {
	readyCondition.Status = metav1.ConditionFalse
	readyCondition.Type = cleanerv1alpha1.ConditionTypeReady
	env, err := cel.NewEnv(opts...)
	if err != nil {
		readyCondition.Reason = cleanerv1alpha1.ConditionReasonEnvironmentError
		readyCondition.Message = "Error preparing CEL environment: " + err.Error()
		return false, false
	}
	condsMet := true
	for cID, c := range conditions {
		compileProgram := func() (cel.Program, error) {
			ast, issues := env.Compile(c)
			if issues != nil && issues.Err() != nil {
				return nil, issues.Err()
			}
			prg, err := env.Program(ast)
			if err != nil {
				return nil, err
			}
			return prg, nil
		}
		prg, err := compileProgram()
		if err != nil {
			readyCondition.Reason = cleanerv1alpha1.ConditionReasonCompileError
			readyCondition.Message = fmt.Sprintf("Error compiling condition %d: %s", cID, err.Error())
			return false, false
		}

		// second return value (details) is always nil without
		// any cel.EvalOptions passed to env.Program
		out, _, err := prg.Eval(celCtx)
		if err != nil {
			readyCondition.Reason = cleanerv1alpha1.ConditionReasonEvaluationError
			readyCondition.Message = fmt.Sprintf("Error evaluating condition %d: %s", cID, err.Error())
			// it is possible for a less than careful condition
			// to have runtime errors sometimes so we must retry
			return false, true
		}

		res, ok := out.Value().(bool)
		if !ok {
			readyCondition.Reason = cleanerv1alpha1.ConditionReasonResultNotBoolean
			readyCondition.Message = fmt.Sprintf("Condition %d result is not a boolean value", cID)
			return false, false
		}
		if !res {
			condsMet = false
		}
	}

	readyCondition.Status = metav1.ConditionTrue
	if !condsMet {
		readyCondition.Reason = cleanerv1alpha1.ConditionReasonWaitingForConditions
		readyCondition.Message = "Waiting for conditions to be met"
		return false, true
	}

	readyCondition.Reason = cleanerv1alpha1.ConditionReasonTerminating
	readyCondition.Message = "Targets resolved and conditions met"
	return true, false
}
