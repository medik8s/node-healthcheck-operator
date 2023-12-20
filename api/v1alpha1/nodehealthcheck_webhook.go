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
	"fmt"
	"reflect"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	OngoingRemediationError   = "prohibited due to running remediation"
	minHealthyError           = "MinHealthy must not be negative"
	invalidSelectorError      = "Invalid selector"
	missingSelectorError      = "Selector is mandatory"
	mandatoryRemediationError = "Either RemediationTemplate or at least one EscalatingRemediations must be set"
	mutualRemediationError    = "RemediationTemplate and EscalatingRemediations usage is mutual exclusive"
	uniqueOrderError          = "EscalatingRemediation Order must be unique"
	uniqueRemediatorError     = "Using multiple templates of same kind is not supported currently"
	minimumTimeoutError       = "EscalatingRemediation Timeout must be at least one minute"
)

// log is for logging in this package.
var nodehealthchecklog = logf.Log.WithName("nodehealthcheck-resource")

func (nhc *NodeHealthCheck) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(nhc).
		Complete()
}

//+kubebuilder:webhook:path=/validate-remediation-medik8s-io-v1alpha1-nodehealthcheck,mutating=false,failurePolicy=fail,sideEffects=None,groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=create;update;delete,versions=v1alpha1,name=vnodehealthcheck.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &NodeHealthCheck{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (nhc *NodeHealthCheck) ValidateCreate() (warnings admission.Warnings, err error) {
	nodehealthchecklog.Info("validate create", "name", nhc.Name)
	return admission.Warnings{}, nhc.validate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (nhc *NodeHealthCheck) ValidateUpdate(old runtime.Object) (warnings admission.Warnings, err error) {
	nodehealthchecklog.Info("validate update", "name", nhc.Name)

	// do the normal validation
	if err := nhc.validate(); err != nil {
		return admission.Warnings{}, err
	}

	// during ongoing remediations, some updates are forbidden
	if nhc.isRemediating() {
		if updated, field := nhc.isRestrictedFieldUpdated(old.(*NodeHealthCheck)); updated {
			return admission.Warnings{}, fmt.Errorf("%s update %s", field, OngoingRemediationError)
		}
	}
	return admission.Warnings{}, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (nhc *NodeHealthCheck) ValidateDelete() (warnings admission.Warnings, err error) {
	nodehealthchecklog.Info("validate delete", "name", nhc.Name)
	if nhc.isRemediating() {
		return admission.Warnings{}, fmt.Errorf("deletion %s", OngoingRemediationError)
	}
	return admission.Warnings{}, nil
}

func (nhc *NodeHealthCheck) validate() error {
	aggregated := errors.NewAggregate([]error{
		nhc.validateMinHealthy(),
		nhc.validateSelector(),
		nhc.validateMutualRemediations(),
		nhc.validateEscalatingRemediations(),
	})

	// everything else should have been covered by API server validation
	// as defined by kubebuilder validation markers on the NHC struct.

	return aggregated
}

func (nhc *NodeHealthCheck) validateMinHealthy() error {
	// Using Minimum kubebuilder marker for IntOrStr does not work (yet)
	if nhc.Spec.MinHealthy == nil {
		return fmt.Errorf("MinHealthy must not be empty")
	}
	if nhc.Spec.MinHealthy.Type == intstr.Int && nhc.Spec.MinHealthy.IntVal < 0 {
		return fmt.Errorf("%s: %v", minHealthyError, nhc.Spec.MinHealthy)
	}
	return nil
}

func (nhc *NodeHealthCheck) validateSelector() error {
	if len(nhc.Spec.Selector.MatchExpressions) == 0 && len(nhc.Spec.Selector.MatchLabels) == 0 {
		return fmt.Errorf(missingSelectorError)
	}
	if _, err := metav1.LabelSelectorAsSelector(&nhc.Spec.Selector); err != nil {
		return fmt.Errorf("%s: %v", invalidSelectorError, err.Error())
	}
	return nil
}

func (nhc *NodeHealthCheck) validateMutualRemediations() error {
	if nhc.Spec.RemediationTemplate == nil && len(nhc.Spec.EscalatingRemediations) == 0 {
		return fmt.Errorf(mandatoryRemediationError)
	}
	if nhc.Spec.RemediationTemplate != nil && len(nhc.Spec.EscalatingRemediations) > 0 {
		return fmt.Errorf(mutualRemediationError)
	}
	return nil
}

func (nhc *NodeHealthCheck) validateEscalatingRemediations() error {
	if nhc.Spec.EscalatingRemediations == nil {
		return nil
	}

	aggregated := errors.NewAggregate([]error{
		nhc.validateEscalatingRemediationsUniqueOrder(),
		nhc.validateEscalatingRemediationsTimeout(),
		nhc.validateEscalatingRemediationsUniqueRemediator(),
	})
	return aggregated
}

func (nhc *NodeHealthCheck) validateEscalatingRemediationsUniqueOrder() error {
	orders := make(map[int]struct{}, len(nhc.Spec.EscalatingRemediations))
	for _, rem := range nhc.Spec.EscalatingRemediations {
		if _, exists := orders[rem.Order]; exists {
			return fmt.Errorf("%s: found duplicate order %v", uniqueOrderError, rem.Order)
		}
		orders[rem.Order] = struct{}{}
	}
	return nil
}

func (nhc *NodeHealthCheck) validateEscalatingRemediationsTimeout() error {
	for _, rem := range nhc.Spec.EscalatingRemediations {
		if rem.Timeout.Duration < 1*time.Minute {
			return fmt.Errorf("%s: found timeout %v", minimumTimeoutError, rem.Timeout)
		}
	}
	return nil
}

func (nhc *NodeHealthCheck) validateEscalatingRemediationsUniqueRemediator() error {
	// this is a workaround until we designed a way to support multiple remediators of same kind
	remediators := make(map[string]struct{}, len(nhc.Spec.EscalatingRemediations))
	for _, rem := range nhc.Spec.EscalatingRemediations {
		if _, exists := remediators[rem.RemediationTemplate.Kind]; exists {
			return fmt.Errorf("%s: duplicate template kind: %v", uniqueRemediatorError, rem.RemediationTemplate.Kind)
		}
		remediators[rem.RemediationTemplate.Kind] = struct{}{}
	}
	return nil
}

func (nhc *NodeHealthCheck) isRestrictedFieldUpdated(old *NodeHealthCheck) (bool, string) {
	// modifying these fields can cause dangling remediations
	if !reflect.DeepEqual(nhc.Spec.Selector, old.Spec.Selector) {
		return true, "selector"
	}
	if !reflect.DeepEqual(nhc.Spec.RemediationTemplate, old.Spec.RemediationTemplate) {
		return true, "remediation template"
	}
	if !reflect.DeepEqual(nhc.Spec.EscalatingRemediations, old.Spec.EscalatingRemediations) {
		return true, "escalating remediations"
	}
	return false, ""
}

func (nhc *NodeHealthCheck) isRemediating() bool {
	return len(nhc.Status.InFlightRemediations) > 0 || len(nhc.Status.UnhealthyNodes) > 0
}
