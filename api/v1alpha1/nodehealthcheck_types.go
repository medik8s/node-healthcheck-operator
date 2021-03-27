/*
Copyright 2021.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// NodeHealthCheckSpec defines the desired state of NodeHealthCheck
type NodeHealthCheckSpec struct {
	// Label selector to match nodes whose health will be exercised.
	// Note: An empty selector will match all nodes.
	// +optional
	Selector metav1.LabelSelector `json:"selector"`

	// UnhealthyConditions contains a list of the conditions that determine
	// whether a node is considered unhealthy.  The conditions are combined in a
	// logical OR, i.e. if any of the conditions is met, the node is unhealthy.
	//
	// +optional
	// +kubebuilder:default:={{type:Ready,status:False,duration:"300s"}}
	UnhealthyConditions []UnhealthyCondition `json:"unhealthyConditions,omitempty"`

	// Any farther remediation is only allowed if at most "MaxUnhealthy" nodes selected by
	// "selector" are not healthy.
	// Expects either a positive integer value or a percentage value.
	// Percentage values must be positive whole numbers and are capped at 100%.
	// Both 0 and 0% are valid and will block all remediation.
	// +kubebuilder:default="49%"
	// +kubebuilder:validation:Pattern="^((100|[0-9]{1,2})%|[0-9]+)$"
	// +kubebuilder:validation:Type=string
	MaxUnhealthy *intstr.IntOrString `json:"maxUnhealthy,omitempty"`

	// ExternalRemediationTemplate is a reference to a remediation template
	// provided by an infrastructure provider.
	//
	// If a node needs remediation the controller will create an object from this template
	// and then it should be picked up by a remediation provider.
	ExternalRemediationTemplate *corev1.ObjectReference `json:"externalRemediationTemplate"`

	// TODO document this
	// +optional
	Backoff *Backoff `json:"backoff,omitempty"`
}

// UnhealthyCondition represents a Node condition type and value with a
// specified duration. When the named condition has been in the given
// status for at least the duration value a node is considered unhealthy.
type UnhealthyCondition struct {
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MinLength=1
	Type corev1.NodeConditionType `json:"type"`

	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MinLength=1
	Status corev1.ConditionStatus `json:"status"`

	// Duration of the condition specified where a node is considered unhealthy.
	// Expects a string of decimal numbers each with optional
	// fraction and a unit suffix, eg "300ms", "1.5h" or "2h45m".
	// Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	// +kubebuilder:validation:Type=string
	Duration metav1.Duration `json:"duration"`
}

// NodeHealthCheckStatus defines the observed state of NodeHealthCheck
type NodeHealthCheckStatus struct {
	//ObservedNodes specified the number of nodes observed by using the NHC spec.selecor
	ObservedNodes int `json:"observedNodes"`

	//HealthyNodes specified the number of healthy nodes observed
	HealthyNodes int `json:"healthyNodes"`

	//TriggeredRemediations records the timestamp when remediation triggered per node
	TriggeredRemediations map[string]times `json:"triggeredRemediations"`
}

type times []metav1.Time

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nodehealthcheck,scope=Cluster
// +kubebuilder:subresource:status

// NodeHealthCheck is the Schema for the nodehealthchecks API
type NodeHealthCheck struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeHealthCheckSpec   `json:"spec,omitempty"`
	Status NodeHealthCheckStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeHealthCheckList contains a list of NodeHealthCheck
type NodeHealthCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeHealthCheck `json:"items"`
}

type Backoff struct {
	//todo rename to strategy instead of type? not sure we need to support more backoff types
	Type BackoffType `json:"type"`
	// Expects a string of decimal numbers each with optional
	// fraction and a unit suffix, eg "300ms", "1.5h" or "2h45m".
	// Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	// +kubebuilder:validation:Type=string
	Limit metav1.Duration `json:"duration"`
}

type BackoffType string

const (
	BackoffTypeExponential BackoffType = "exponential"
)

func init() {
	SchemeBuilder.Register(&NodeHealthCheck{}, &NodeHealthCheckList{})
}
