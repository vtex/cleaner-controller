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

// RetryConfig defines how the controller will retry the condition.
type RetryConfig struct {
	// Period defines how long the controller should wait before retrying
	// the condition.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	Period *metav1.Duration `json:"period"`
}

// HelmConfig defines the helm release associated with the targetted resources
// and whether the release should be deleted.
type HelmConfig struct {
	// Release is the Helm release name.
	Release string `json:"release,omitempty"`

	// Delete specifies whether the Helm release should be deleted
	// whenever the ConditionalTTL is triggered.
	Delete bool `json:"delete,omitempty"`
}

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

// Target declares how to find one or more resources related to the ConditionalTTL.
// Targets are watched in order to trigger a reevaluation of the conditions and, when
// the ConditionalTTl is triggered they might be deleted by the controller.
type Target struct {
	// Name identifies this target group and identifies the target current
	// state when evaluating the CEL conditions.
	// The name `time` is invalid and is included by default during evaluation.
	// +kubebuilder:validation:Pattern=`^[^t].*|t($|[^i]).*|ti($|[^m]).*|tim($|[^e]).*|time.+`
	Name string `json:"name"`

	// Delete specifies whether this target group should be deleted
	// when the ConditionalTTL is triggered.
	Delete bool `json:"delete"`

	// IncludeWhenEvaluating specifies whether this target group should be
	// included in the CEL evaluation context.
	IncludeWhenEvaluating bool `json:"includeWhenEvaluating"`

	// Reference declares how to find either a single object, through its name,
	// or a collection, through LabelSelectors.
	Reference TargetReference `json:"reference"`
}

// ConditionalTTLSpec defines the desired state of ConditionalTTL
type ConditionalTTLSpec struct {
	// TTL specifies the minimum duration the target objects will last
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	TTL *metav1.Duration `json:"ttl"`

	// +optional
	Retry *RetryConfig `json:"retry,omitempty"`

	// +optional
	Helm *HelmConfig `json:"helm,omitempty"`

	Targets []Target `json:"targets,omitempty"`

	// +optional
	Conditions []string `json:"conditions,omitempty"`

	// TODO: validate https? protocol
	// +optional
	CloudEventSink *string `json:"cloudEventSink,omitempty"`
}

type TargetStatus struct {
	// Name matches the declared name on Spec.Targets.
	Name string `json:"name"`

	Delete bool `json:"delete"`

	IncludeWhenEvaluating bool `json:"includeWhenEvaluating"`

	// State is the observed state of the target on the cluster
	// when deletion began.
	//+kubebuilder:pruning:PreserveUnknownFields
	State *unstructured.Unstructured `json:"state,omitempty"`
}

// ConditionalTTLStatus defines the observed state of ConditionalTTL
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
// ConditionalTTL is the Schema for the conditionalttls API
type ConditionalTTL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConditionalTTLSpec   `json:"spec,omitempty"`
	Status ConditionalTTLStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConditionalTTLList contains a list of ConditionalTTL
type ConditionalTTLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConditionalTTL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConditionalTTL{}, &ConditionalTTLList{})
}
