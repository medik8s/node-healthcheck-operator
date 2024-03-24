package annotations

import (
	commonannotations "github.com/medik8s/common/pkg/annotations"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MHCPausedAnnotation is an annotation that can be applied to MachineHealthCheck objects to prevent the MHC
	// controller from processing it.
	MHCPausedAnnotation = "cluster.x-k8s.io/paused"
	// TemplateNameAnnotation is an annotation that will be placed on the CRs of remediatiors who support multiple templates of the same remediator.
	// This is done because when checking for timeout CRs we need to know whether a CR was already created or not by that template.
	TemplateNameAnnotation = "remediation.medik8s.io/template-name"
)

// HasMultipleTemplatesAnnotation returns true if the object has the medik8s `multiple-templates-support` annotation.
func HasMultipleTemplatesAnnotation(o metav1.Object) bool {
	return hasAnnotation(o, commonannotations.MultipleTemplatesSupportedAnnotation)
}

func HasMHCPausedAnnotation(o metav1.Object) bool {
	return hasAnnotation(o, MHCPausedAnnotation)
}

// hasAnnotation returns true if the object has the specified annotation.
func hasAnnotation(o metav1.Object, annotation string) bool {
	annotations := o.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, ok := annotations[annotation]
	return ok
}
