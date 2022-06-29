package utils

import (
	"fmt"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ValidateMinHealthy(nhc *v1alpha1.NodeHealthCheck) error {
	if nhc.Spec.MinHealthy == nil {
		return fmt.Errorf("MinHealthy is nil")
	}
	if nhc.Spec.MinHealthy.Type == intstr.Int && nhc.Spec.MinHealthy.IntVal < 0 {
		return fmt.Errorf("MinHealthy is negative: %v", nhc.Spec.MinHealthy)
	}

	// everything else should have been covered by API server validation
	// as defined by kubebuilder validation markers on the NHC struct.
	// Using Minimum for IntOrStr does not work (yet/)

	return nil
}
