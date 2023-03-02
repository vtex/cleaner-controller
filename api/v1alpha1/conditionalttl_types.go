/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// RetryConfig defines how the controller should retry evaluating the
// set of conditions.
type RetryConfig struct {
	// Period defines how long the controller should wait before retrying
	// the condition.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	Period *metav1.Duration `json:"period"`
}

// HelmConfig specifies a Helm release by its name and whether
// the release should be deleted.
type HelmConfig struct {
	// The Helm Release name.
	Release string `json:"release,omitempty"`

	// Delete specifies whether the Helm release should be deleted.
	Delete bool `json:"delete,omitempty"`
}

// TargetReference declares how a target group should be looked up.
// A target group can reference either a single Kubernetes resource - in which case
// finding it is required in other to evaluate the set of conditions - or
// a collection of resources of the same GroupVersionKind. In contrast
// with single targets, an empty collection is a valid value when evaluating
// the set of conditions.
type TargetReference struct {
	// TODO: apiVersion and kind of TypeMeta are optional, can they be made
	// required without duplicating it?
	metav1.TypeMeta `json:",inline"`

	// Name matches a single object. If name is specified, LabelSelector
	// is ignored.
	// +optional
	Name *string `json:"name"`

	// LabelSelector allows more than one object to be included in the target
	// group. If Name is not empty, LabelSelector is ignored.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector"`
}

// Target declares how to find one or more resources related to the ConditionalTTL,
// whether they should be deleted and whether they are necessary for evaluating the
// set of conditions.
type Target struct {
	// Name identifies this target group and is used to refer to its state
	// when evaluating the set of conditions.
	// The name `time` is invalid and is included by default during evaluation.
	// +kubebuilder:validation:Pattern=`^[^t].*|t($|[^i]).*|ti($|[^m]).*|tim($|[^e]).*|time.+`
	Name string `json:"name"`

	// Delete indicates whether this target group should be deleted
	// when the ConditionalTTL is triggered.
	Delete bool `json:"delete"`

	// IncludeWhenEvaluating indicates whether this target group should be
	// included in the CEL evaluation context.
	IncludeWhenEvaluating bool `json:"includeWhenEvaluating"`

	// Reference declares how to find either a single object, using its name,
	// or a collection, using a LabelSelector.
	Reference TargetReference `json:"reference"`
}

// ConditionalTTLSpec represents the configuration for a ConditionalTTL object.
// A ConditionalTTL's specification is the union of conditions under which
// deletion begins and actions to be taken during it.
type ConditionalTTLSpec struct {
	// Duration the controller should wait relative to the ConditionalTTL's CreationTime
	// before starting deletion.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	TTL *metav1.Duration `json:"ttl"`

	// Specifies how the controller should retry the evaluation of conditions.
	// This field is required when the list of conditions is not empty.
	// +optional
	Retry *RetryConfig `json:"retry,omitempty"`

	// Optional: Allows a ConditionalTTL to refer to and possibly delete a Helm release,
	// usually the release responsible for creating the targets of the ConditionalTTL.
	// +optional
	Helm *HelmConfig `json:"helm,omitempty"`

	// List of targets the ConditionalTTL is interested in deleting or that are needed
	// for evaluating the conditions under which deletion should take place.
	Targets []Target `json:"targets,omitempty"`

	// Optional list of [Common Expression Language](https://github.com/google/cel-spec) conditions
	// which should all evaluate to true before deletion takes place.
	// +optional
	Conditions []string `json:"conditions,omitempty"`

	// Optional http(s) address the controller should send a [Cloud Event](https://github.com/cloudevents/spec/blob/main/cloudevents/spec.md)
	// to after deletion takes place.
	// +optional
	CloudEventSink *string `json:"cloudEventSink,omitempty"`
}

type TargetStatus struct {
	// Name is the target name as declared on `spec.targets`.
	Name string `json:"name"`

	// Delete matches `.spec.targets.delete` for the target
	// identified by `name`.
	Delete bool `json:"delete"`

	// IncludeWhenEvaluating matches `.spec.targets.includeWhenEvaluating` for the target
	// identified by `name`.
	IncludeWhenEvaluating bool `json:"includeWhenEvaluating"`

	// State is the observed state of the target on the cluster
	// when deletion began.
	//+kubebuilder:pruning:PreserveUnknownFields
	State *unstructured.Unstructured `json:"state,omitempty"`
}

// ConditionalTTLStatus defines the observed state of ConditionalTTL.
type ConditionalTTLStatus struct {
	Targets []TargetStatus `json:"targets,omitempty"`

	// EvaluationTime is the time when the conditions for deletion were met.
	EvaluationTime *metav1.Time `json:"evaluationTime,omitempty"`

	//+optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=cttl
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="TTL",type=string,format=date-time,JSONPath=`.spec.ttl`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`

// ConditionalTTL allows one to declare a set of conditions under which a set of
// resources should be deleted.
//
// The ConditionalTTL's controller will track the statuses of its referenced Targets,
// periodically re-evaluating the declared conditions for deletion.
type ConditionalTTL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConditionalTTLSpec   `json:"spec,omitempty"`
	Status ConditionalTTLStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConditionalTTLList contains a list of ConditionalTTL.
type ConditionalTTLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConditionalTTL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConditionalTTL{}, &ConditionalTTLList{})
}
