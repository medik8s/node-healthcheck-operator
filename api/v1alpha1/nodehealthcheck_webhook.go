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
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
)

const (
	OngoingRemediationError    = "prohibited due to running remediation"
	minHealthyError            = "minHealthy must not be negative"
	maxUnhealthyError          = "maxUnhealthy must not be negative"
	invalidSelectorError       = "Invalid selector"
	missingSelectorError       = "Selector is mandatory"
	mandatoryRemediationError  = "Either RemediationTemplate or at least one EscalatingRemediations must be set"
	mutualRemediationError     = "RemediationTemplate and EscalatingRemediations usage is mutual exclusive"
	uniqueOrderError           = "EscalatingRemediation Order must be unique"
	uniqueRemediatorError      = "Using multiple templates of same kind is not supported for this template"
	minimumTimeoutError        = "EscalatingRemediation Timeout must be at least one minute"
	unsupportedCpTopologyError = "Unsupported control plane topology"
)

// log is for logging in this package.
var nodehealthchecklog = logf.Log.WithName("nodehealthcheck-resource")

func (nhc *NodeHealthCheck) SetupWebhookWithManager(mgr ctrl.Manager, caps *cluster.Capabilities) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(nhc).
		WithValidator(&customValidator{mgr.GetClient(), caps}).
		Complete()
}

//+kubebuilder:webhook:path=/validate-remediation-medik8s-io-v1alpha1-nodehealthcheck,mutating=false,failurePolicy=fail,sideEffects=None,groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=create;update;delete,versions=v1alpha1,name=vnodehealthcheck.kb.io,admissionReviewVersions=v1

type customValidator struct {
	client.Client
	caps *cluster.Capabilities
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *customValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	nhc := obj.(*NodeHealthCheck)
	nodehealthchecklog.Info("validate create", "name", nhc.Name)
	return admission.Warnings{}, v.validate(ctx, nhc)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *customValidator) ValidateUpdate(ctx context.Context, old runtime.Object, new runtime.Object) (warnings admission.Warnings, err error) {
	nhc := new.(*NodeHealthCheck)
	nodehealthchecklog.Info("validate update", "name", nhc.Name)

	// do the normal validation
	if err := v.validate(ctx, nhc); err != nil {
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
func (v *customValidator) ValidateDelete(_ context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	nhc := obj.(*NodeHealthCheck)
	nodehealthchecklog.Info("validate delete", "name", nhc.Name)
	if nhc.isRemediating() {
		return admission.Warnings{}, fmt.Errorf("deletion %s", OngoingRemediationError)
	}
	return admission.Warnings{}, nil
}

func (v *customValidator) validate(ctx context.Context, nhc *NodeHealthCheck) error {
	aggregated := errors.NewAggregate([]error{
		ValidateMinHealthyMaxUnhealthy(nhc),
		v.validateSelector(nhc),
		v.validateMutualRemediations(nhc),
		v.validateEscalatingRemediations(ctx, nhc),
		v.validateControlPlaneTopology(),
	})

	// everything else should have been covered by API server validation
	// as defined by kubebuilder validation markers on the NHC struct.

	return aggregated
}

func (v *customValidator) validateControlPlaneTopology() error {
	if !v.caps.IsSupportedControlPlaneTopology {
		return fmt.Errorf(unsupportedCpTopologyError)
	}
	return nil
}

func (v *customValidator) validateSelector(nhc *NodeHealthCheck) error {
	if len(nhc.Spec.Selector.MatchExpressions) == 0 && len(nhc.Spec.Selector.MatchLabels) == 0 {
		return fmt.Errorf(missingSelectorError)
	}
	if _, err := metav1.LabelSelectorAsSelector(&nhc.Spec.Selector); err != nil {
		return fmt.Errorf("%s: %v", invalidSelectorError, err.Error())
	}
	return nil
}

func (v *customValidator) validateMutualRemediations(nhc *NodeHealthCheck) error {
	if nhc.Spec.RemediationTemplate == nil && len(nhc.Spec.EscalatingRemediations) == 0 {
		return fmt.Errorf(mandatoryRemediationError)
	}
	if nhc.Spec.RemediationTemplate != nil && len(nhc.Spec.EscalatingRemediations) > 0 {
		return fmt.Errorf(mutualRemediationError)
	}
	return nil
}

func (v *customValidator) validateEscalatingRemediations(ctx context.Context, nhc *NodeHealthCheck) error {
	if nhc.Spec.EscalatingRemediations == nil {
		return nil
	}

	aggregated := errors.NewAggregate([]error{
		v.validateEscalatingRemediationsUniqueOrder(nhc),
		v.validateEscalatingRemediationsTimeout(nhc),
		v.validateEscalatingRemediationsUniqueRemediator(ctx, nhc),
	})
	return aggregated
}

func (v *customValidator) validateEscalatingRemediationsUniqueOrder(nhc *NodeHealthCheck) error {
	orders := make(map[int]struct{}, len(nhc.Spec.EscalatingRemediations))
	for _, rem := range nhc.Spec.EscalatingRemediations {
		if _, exists := orders[rem.Order]; exists {
			return fmt.Errorf("%s: found duplicate order %v", uniqueOrderError, rem.Order)
		}
		orders[rem.Order] = struct{}{}
	}
	return nil
}

func (v *customValidator) validateEscalatingRemediationsTimeout(nhc *NodeHealthCheck) error {
	for _, rem := range nhc.Spec.EscalatingRemediations {
		if rem.Timeout.Duration < 1*time.Minute {
			return fmt.Errorf("%s: found timeout %v", minimumTimeoutError, rem.Timeout)
		}
	}
	return nil
}

func (v *customValidator) validateEscalatingRemediationsUniqueRemediator(ctx context.Context, nhc *NodeHealthCheck) error {
	remediators := make(map[string]struct{}, len(nhc.Spec.EscalatingRemediations))
	for _, rem := range nhc.Spec.EscalatingRemediations {
		kind := rem.RemediationTemplate.Kind
		if _, exists := remediators[kind]; exists && !v.isMultipleTemplatesSupported(ctx, rem.RemediationTemplate) {
			return fmt.Errorf("%s: duplicate template kind: %v", uniqueRemediatorError, kind)
		}
		remediators[kind] = struct{}{}
	}
	return nil
}

func (v *customValidator) isMultipleTemplatesSupported(ctx context.Context, nhcExpectedTemplate corev1.ObjectReference) bool {
	templateCRBase := &unstructured.Unstructured{}
	templateCRBase.SetGroupVersionKind(nhcExpectedTemplate.GroupVersionKind())
	templateList := &unstructured.UnstructuredList{Object: templateCRBase.Object}

	if err := v.Client.List(ctx, templateList); err != nil || len(templateList.Items) == 0 {
		nodehealthchecklog.Error(err, "failed to fetch CR Templates", "template kind", nhcExpectedTemplate.GroupVersionKind().Kind)
		return false
	}

	for _, actualTemplate := range templateList.Items {
		if !annotations.HasMultipleTemplatesAnnotation(&actualTemplate) {
			return false
		}
	}

	return true
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
	for _, unhealthyNode := range nhc.Status.UnhealthyNodes {
		if len(unhealthyNode.Remediations) > 0 {
			return true
		}
	}
	return false
}

func ValidateMinHealthyMaxUnhealthy(nhc *NodeHealthCheck) error {
	// Using Minimum kubebuilder marker for IntOrStr does not work (yet)
	if nhc.Spec.MinHealthy != nil && nhc.Spec.MaxUnhealthy != nil {
		return fmt.Errorf("minHealthy and maxUnhealthy cannot be specified at the same time")
	}
	if nhc.Spec.MinHealthy == nil && nhc.Spec.MaxUnhealthy == nil {
		return fmt.Errorf("one of minHealthy and maxUnhealthy should be specified")
	}
	if nhc.Spec.MinHealthy != nil && nhc.Spec.MinHealthy.Type == intstr.Int && nhc.Spec.MinHealthy.IntVal < 0 {
		return fmt.Errorf("%s: %v", minHealthyError, nhc.Spec.MinHealthy)
	}
	if nhc.Spec.MaxUnhealthy != nil && nhc.Spec.MaxUnhealthy.Type == intstr.Int && nhc.Spec.MaxUnhealthy.IntVal < 0 {
		return fmt.Errorf("%s: %v", maxUnhealthyError, nhc.Spec.MaxUnhealthy)
	}
	if nhc.Spec.MaxUnhealthy != nil &&
		nhc.Spec.RemediationTemplate != nil &&
		strings.EqualFold(nhc.Spec.RemediationTemplate.Kind, "MachineDeletionRemediationTemplate") {
		return fmt.Errorf("maxUnhealthy can't be used with MachineDeletionRemediationTemplate")
	}
	if nhc.Spec.MaxUnhealthy != nil && nhc.Spec.EscalatingRemediations != nil {
		for _, erem := range nhc.Spec.EscalatingRemediations {
			if strings.EqualFold(erem.RemediationTemplate.Kind, "MachineDeletionRemediationTemplate") {
				return fmt.Errorf("maxUnhealthy can't be used with MachineDeletionRemediationTemplate escalating remediation")
			}
		}
	}
	return nil
}
