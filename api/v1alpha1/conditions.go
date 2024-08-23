package v1alpha1

const (
	ConditionReasonNotExpired           = "NotExpired"
	ConditionReasonKeepMinimumAmount    = "KeepMinimumAmount"
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
