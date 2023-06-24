package utils

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// MHCPausedAnnotation is an annotation that can be applied to MachineHealthCheck objects to prevent the MHC
	// controller from processing it.
	MHCPausedAnnotation = "cluster.x-k8s.io/paused"
)

// HasMHCPausedAnnotation returns true if the object has the MHC `paused` annotation.
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
