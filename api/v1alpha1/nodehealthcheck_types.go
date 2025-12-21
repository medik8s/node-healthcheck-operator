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

const (
	// ConditionTypeDisabled is the condition type used when NHC will get disabled
	ConditionTypeDisabled = "Disabled"
	// ConditionReasonDisabledMHC is the condition reason for type Disabled in case NHC is disabled because
	// of conflicts with MHC
	ConditionReasonDisabledMHC = "ConflictingMachineHealthCheckDetected"
	// ConditionReasonDisabledTemplateNotFound is the reason for type Disabled when the template wasn't found
	ConditionReasonDisabledTemplateNotFound = "RemediationTemplateNotFound"
	// ConditionReasonDisabledTemplateInvalid is the reason for type Disabled when the template is invalid
	ConditionReasonDisabledTemplateInvalid = "RemediationTemplateInvalid"
	// ConditionReasonEnabled is the condition reason for type Disabled and status False
	ConditionReasonEnabled = "NodeHealthCheckEnabled"

	// ConditionTypeStormActive is the condition type used when NHC will get disabled
	ConditionTypeStormActive = "StormActive"
	// ConditionReasonStormThresholdChange is the condition reason for a storm change from active to inactive and vice versa
	ConditionReasonStormThresholdChange = "HealthyNodeThresholdChange"
)

// NHCPhase is the string used for NHC.Status.Phase
type NHCPhase string

const (
	// PhaseDisabled is used when the Disabled condition is true
	PhaseDisabled NHCPhase = "Disabled"

	// PhasePaused is used when not disabled, but PauseRequests is set
	PhasePaused NHCPhase = "Paused"

	// PhaseRemediating is used when not disabled and not paused, and InFlightRemediations is set
	PhaseRemediating NHCPhase = "Remediating"

	// PhaseEnabled is used in all other cases
	PhaseEnabled NHCPhase = "Enabled"
)

// NodeHealthCheckSpec defines the desired state of NodeHealthCheck
type NodeHealthCheckSpec struct {
	// Label selector to match nodes whose health will be exercised.
	//
	// Selecting both control-plane and worker nodes in one NHC CR is
	// highly discouraged and can result in undesired behaviour.
	//
	// Note: mandatory now for above reason, but for backwards compatibility existing
	// CRs will continue to work with an empty selector, which matches all nodes.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Selector metav1.LabelSelector `json:"selector"`

	// UnhealthyConditions contains a list of the conditions that determine
	// whether a node is considered unhealthy.  The conditions are combined in a
	// logical OR, i.e. if any of the conditions is met, the node is unhealthy.
	//
	//+optional
	//+listType=map
	//+listMapKey=type
	//+listMapKey=status
	//+kubebuilder:default:={{type:Ready,status:False,duration:"300s"},{type:Ready,status:Unknown,duration:"300s"}}
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	UnhealthyConditions []UnhealthyCondition `json:"unhealthyConditions,omitempty"`

	// Remediation is allowed if at least "MinHealthy" nodes selected by "selector" are healthy.
	// Expects either a non-negative integer value or a percentage value.
	// Percentage values must be positive whole numbers and are capped at 100%.
	// 100% is valid and will block all remediation.
	// MinHealthy and MaxUnhealthy are configuring the same aspect,
	// and they cannot be used at the same time.
	//
	//+optional
	//+kubebuilder:validation:XIntOrString
	//+kubebuilder:validation:Pattern="^((100|[0-9]{1,2})%|[0-9]+)$"
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	MinHealthy *intstr.IntOrString `json:"minHealthy,omitempty"`

	// Remediation is allowed if no more than "MaxUnhealthy" nodes selected by "selector" are not healthy.
	// Expects either a non-negative integer value or a percentage value.
	// Percentage values must be positive whole numbers and are capped at 100%.
	// 0% is valid and will block all remediation.
	// MaxUnhealthy should not be used with remediators that delete nodes (e.g. MachineDeletionRemediation),
	// as this breaks the logic for counting healthy and unhealthy nodes.
	// MinHealthy and MaxUnhealthy are configuring the same aspect,
	// and they cannot be used at the same time.
	//
	//+optional
	//+kubebuilder:validation:XIntOrString
	//+kubebuilder:validation:Pattern="^((100|[0-9]{1,2})%|[0-9]+)$"
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	MaxUnhealthy *intstr.IntOrString `json:"maxUnhealthy,omitempty"`

	// StormTerminationDelay introduces a configurable delay after storm recovery
	// exit criteria are satisfied (for example, when the number of healthy nodes
	// rises above the configured minHealthy constraint). While this
	// delay is in effect, NHC remains in storm recovery mode and does not create
	// new remediations. Once the delay elapses, storm recovery mode exits and normal
	// remediation resumes.
	//
	// Expects a string of decimal numbers each with optional fraction and a unit
	// suffix, e.g. "300ms", "1.5h" or "2h45m". Valid time units are "ns", "us"
	// (or "µs"), "ms", "s", "m", "h".
	//
	//+kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	//+kubebuilder:validation:Type=string
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	StormTerminationDelay *metav1.Duration `json:"stormTerminationDelay,omitempty"`

	// RemediationTemplate is a reference to a remediation template
	// provided by an infrastructure provider.
	//
	// If a node needs remediation the controller will create an object from this template
	// and then it should be picked up by a remediation provider.
	//
	// Mutually exclusive with EscalatingRemediations
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	RemediationTemplate *corev1.ObjectReference `json:"remediationTemplate,omitempty"`

	// EscalatingRemediations contain a list of ordered remediation templates with a timeout.
	// The remediation templates will be used one after another, until the unhealthy node
	// gets healthy within the timeout of the currently processed remediation. The order of
	// remediation is defined by the "order" field of each "escalatingRemediation".
	//
	// Mutually exclusive with RemediationTemplate
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	EscalatingRemediations []EscalatingRemediation `json:"escalatingRemediations,omitempty"`

	// PauseRequests will prevent any new remediation to start, while in-flight remediations
	// keep running. Each entry is free form, and ideally represents the requested party reason
	// for this pausing - i.e:
	//     "imaginary-cluster-upgrade-manager-operator"
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	PauseRequests []string `json:"pauseRequests,omitempty"`

	// HealthyDelay is the time before NHC would allow a node to be healthy again.
	// A negative value means that NHC will never consider the node healthy and a manual intervention is expected
	//
	//+kubebuilder:validation:Pattern="^-?([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	//+kubebuilder:validation:Type=string
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	HealthyDelay *metav1.Duration `json:"healthyDelay,omitempty"`
}

// UnhealthyCondition represents a Node condition type and value with a
// specified duration. When the named condition has been in the given
// status for at least the duration value a node is considered unhealthy.
type UnhealthyCondition struct {
	// The condition type in the node's status to watch for.
	//
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:MinLength=1
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Type corev1.NodeConditionType `json:"type"`

	// The condition status in the node's status to watch for.
	// Typically False, True or Unknown.
	//
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:MinLength=1
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Status corev1.ConditionStatus `json:"status"`

	// Duration of the condition specified when a node is considered unhealthy.
	//
	// Expects a string of decimal numbers each with optional
	// fraction and a unit suffix, eg "300ms", "1.5h" or "2h45m".
	// Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".
	//
	//+kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	//+kubebuilder:validation:Type=string
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Duration metav1.Duration `json:"duration"`
}

// EscalatingRemediation defines a remediation template with order and timeout
type EscalatingRemediation struct {
	// RemediationTemplate is a reference to a remediation template
	// provided by a remediation provider.
	//
	// If a node needs remediation the controller will create an object from this template
	// and then it should be picked up by a remediation provider.
	//
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	RemediationTemplate corev1.ObjectReference `json:"remediationTemplate"`

	// Order defines the order for this remediation.
	// Remediations with lower order will be used before remediations with higher order.
	// Remediations must not have the same order.
	//
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Order int `json:"order"`

	// Timeout defines how long NHC will wait for the node getting healthy
	// before the next remediation (if any) will be used. When the last remediation times out,
	// the overall remediation is considered as failed.
	// As a safeguard for preventing parallel remediations, a minimum of 60s is enforced.
	//
	// Expects a string of decimal numbers each with optional
	// fraction and a unit suffix, eg "300ms", "1.5h" or "2h45m".
	// Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".
	//
	//+kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	//+kubebuilder:validation:Type=string
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Timeout metav1.Duration `json:"timeout"`
}

// NodeHealthCheckStatus defines the observed state of NodeHealthCheck
type NodeHealthCheckStatus struct {
	// ObservedNodes specified the number of nodes observed by using the NHC spec.selector
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	ObservedNodes *int `json:"observedNodes,omitempty"`

	// HealthyNodes specified the number of healthy nodes observed
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	HealthyNodes *int `json:"healthyNodes,omitempty"`

	// UnhealthyNodes tracks currently unhealthy nodes and their remediations.
	//
	//+listType=map
	//+listMapKey=name
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	UnhealthyNodes []*UnhealthyNode `json:"unhealthyNodes,omitempty"`

	// InFlightRemediations records the timestamp when remediation triggered per node.
	// Deprecated in favour of UnhealthyNodes.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	InFlightRemediations map[string]metav1.Time `json:"inFlightRemediations,omitempty"`

	// Represents the observations of a NodeHealthCheck's current state.
	// Known .status.conditions.type are: "Disabled"
	//
	//+listType=map
	//+listMapKey=type
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase represents the current phase of this Config.
	// Known phases are Disabled, Paused, Remediating and Enabled, based on:\n
	// - the status of the Disabled condition\n
	// - the value of PauseRequests\n
	// - the value of InFlightRemediations
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors="urn:alm:descriptor:io.kubernetes.phase"
	Phase NHCPhase `json:"phase,omitempty"`

	// Reason explains the current phase in more detail.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors="urn:alm:descriptor:io.kubernetes.phase:reason"
	Reason string `json:"reason,omitempty"`

	// StormRecoveryStartTime records when storm recovery mode was activated.
	// This field is set when StormRecoveryActive becomes true and helps track
	// how long the system has been in storm recovery mode.
	//
	//+optional
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:Format=date-time
	//+operator-sdk:csv:customresourcedefinitions:type=status
	StormRecoveryStartTime *metav1.Time `json:"stormRecoveryStartTime,omitempty"`

	// StormTerminationStartTime records when storm recovery mode regained the minHealthy/maxUnhealthy constraint
	// and the storm is about to end (after NodeHealthCheckSpec.StormTerminationDelay has passed).
	//
	//+optional
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:Format=date-time
	//+operator-sdk:csv:customresourcedefinitions:type=status
	StormTerminationStartTime *metav1.Time `json:"stormTerminationStartTime,omitempty"`

	// LastUpdateTime is the last time the status was updated.
	//
	//+optional
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:Format=date-time
	//+operator-sdk:csv:customresourcedefinitions:type=status
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

// UnhealthyNode defines an unhealthy node and its remediations
type UnhealthyNode struct {
	// Name is the name of the unhealthy node
	//
	//+operator-sdk:csv:customresourcedefinitions:type=status
	Name string `json:"name"`

	// Remediations tracks the remediations created for this node
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	Remediations []*Remediation `json:"remediations,omitempty"`

	// ConditionsHealthyTimestamp is RFC 3339 date and time at which the unhealthy conditions didn't match anymore.
	// The remediation CR will be deleted at that time, but the node will still be tracked as unhealthy until all
	// remediation CRs are actually deleted, when remediators finished cleanup and removed their finalizers.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	ConditionsHealthyTimestamp *metav1.Time `json:"conditionsHealthyTimestamp,omitempty"`

	// HealthyDelayed notes whether a node should be considered healthy, but isn't due to NodeHealthCheckSpec.HealthyDelay configuration.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	HealthyDelayed *bool `json:"healthyDelayed,omitempty"`
}

// Remediation defines a remediation which was created for a node
type Remediation struct {
	// Resource is the reference to the remediation CR which was created
	//
	//+operator-sdk:csv:customresourcedefinitions:type=status
	Resource corev1.ObjectReference `json:"resource"`

	// Started is the creation time of the remediation CR
	//
	//+operator-sdk:csv:customresourcedefinitions:type=status
	Started metav1.Time `json:"started"`

	// TimedOut is the time when the remediation timed out.
	// Applicable for escalating remediations only.
	//
	//+optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	TimedOut *metav1.Time `json:"timedOut,omitempty"`

	// TemplateName is required when using several templates of the same kind
	// +optional
	//+operator-sdk:csv:customresourcedefinitions:type=status
	TemplateName string `json:"templateName,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=nodehealthchecks,scope=Cluster,shortName=nhc
//+kubebuilder:subresource:status

// NodeHealthCheck is the Schema for the nodehealthchecks API
//
// +operator-sdk:csv:customresourcedefinitions:resources={{"NodeHealthCheck","v1alpha1","nodehealthchecks"}}
type NodeHealthCheck struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeHealthCheckSpec   `json:"spec,omitempty"`
	Status NodeHealthCheckStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NodeHealthCheckList contains a list of NodeHealthCheck
type NodeHealthCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeHealthCheck `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeHealthCheck{}, &NodeHealthCheckList{})
}
