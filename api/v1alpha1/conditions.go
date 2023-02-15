package v1alpha1

const (
	ConditionReasonNotExpired           = "NotExpired"
	ConditionReasonTargetResolveError   = "TargetResolveError"
	ConditionReasonEnvironmentError     = "ConditionEnvironmentError"
	ConditionReasonCompileError         = "ConditionCompileError"
	ConditionReasonEvaluationError      = "ConditionEvaluationError"
	ConditionReasonResultNotBoolean     = "ConditionResultNotBoolean"
	ConditionReasonWaitingForConditions = "WaitingForConditions"
	ConditionReasonTerminating          = "Terminating"
)

const (
	ConditionTypeReady = "Ready"
)
