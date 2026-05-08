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
	"strings"

	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	minHealthyError   = "minHealthy must not be negative"
	maxUnhealthyError = "maxUnhealthy must not be negative"
)

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
