package resources

import (
	"fmt"

	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

const (
	metal3RemediationTemplateKind = "Metal3RemediationTemplate"
	machineAPINamespace           = "openshift-machine-api"
)

type brokenTemplate struct{ msg string }

func (bt brokenTemplate) Error() string { return bt.msg }

func (m *manager) GetTemplate(templateRef *v1.ObjectReference) (*unstructured.Unstructured, error) {
	template := new(unstructured.Unstructured)
	template.SetGroupVersionKind(templateRef.GroupVersionKind())
	template.SetName(templateRef.Name)
	template.SetNamespace(templateRef.Namespace)
	if err := m.Get(m.ctx, client.ObjectKeyFromObject(template), template); err != nil {
		return nil, errors.Wrapf(err, "failed to get external remediation template %s/%s", template.GetNamespace(), template.GetName())
	}

	// check if template is valid
	_, found, err := unstructured.NestedMap(template.Object, "spec", "template")
	if !found || err != nil {
		return nil, brokenTemplate{fmt.Sprintf("invalid template %s/%s, didn't find spec.template.spec", template.GetNamespace(), template.GetName())}
	}
	return template, nil
}

// ValidateTemplates only returns an error when we don't know whether the template is valid or not, for triggering a requeue with backoff
func (m *manager) ValidateTemplates(nhc *remediationv1alpha1.NodeHealthCheck) (valid bool, reason, message string, err error) {
	if templateRef := nhc.Spec.RemediationTemplate; templateRef != nil {
		template, templateError := m.GetTemplate(templateRef)
		valid, reason, message, err = m.validateTemplate(template, templateError)
		if !valid || err != nil {
			return
		}
	}
	for _, escRem := range nhc.Spec.EscalatingRemediations {
		templateRef := escRem.RemediationTemplate
		template, templateError := m.GetTemplate(&templateRef)
		valid, reason, message, err = m.validateTemplate(template, templateError)
		if !valid || err != nil {
			return
		}
	}
	return
}

func (m *manager) validateTemplate(template *unstructured.Unstructured, templateError error) (valid bool, reason, message string, err error) {
	if templateError != nil {
		cause := errors.Cause(templateError)
		if apierrors.IsNotFound(cause) || meta.IsNoMatchError(cause) {
			return false,
				remediationv1alpha1.ConditionReasonDisabledTemplateNotFound,
				fmt.Sprintf("Remediation template not found: %q", templateError.Error()),
				nil
		} else if _, ok := templateError.(brokenTemplate); ok {
			return false,
				remediationv1alpha1.ConditionReasonDisabledTemplateInvalid,
				fmt.Sprintf("Remediation template is invalid: %q", templateError.Error()),
				nil
		}
		return false, "", "", templateError
	}
	if template != nil {
		// Metal3 remediation needs the node's machine as owner ref,
		// and owners need to be in the same namespace as their dependent.
		// Make sure that the template is in the Machine's namespace.
		if template.GetKind() == metal3RemediationTemplateKind && template.GetNamespace() != machineAPINamespace {
			return false,
				remediationv1alpha1.ConditionReasonDisabledTemplateInvalid,
				fmt.Sprintf("Metal3RemediationTemplate must be in the openshift-machine-api namespace. It is configured to be in namespace: %s", template.GetNamespace()),
				nil
		}
	}
	return true, "", "", nil
}
