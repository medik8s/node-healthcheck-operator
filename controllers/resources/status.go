package resources

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

func UpdateStatusRemediationStarted(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, remediationCR *unstructured.Unstructured) {
	if _, exists := nhc.Status.InFlightRemediations[remediationCR.GetName()]; !exists {
		if nhc.Status.InFlightRemediations == nil {
			nhc.Status.InFlightRemediations = make(map[string]metav1.Time, 1)
		}
		if _, ok := nhc.Status.InFlightRemediations[node.GetName()]; !ok {
			nhc.Status.InFlightRemediations[node.GetName()] = remediationCR.GetCreationTimestamp()
		}
	}

	remediation := remediationv1alpha1.Remediation{
		Resource: corev1.ObjectReference{
			Kind:       remediationCR.GetKind(),
			Namespace:  remediationCR.GetNamespace(),
			Name:       remediationCR.GetName(),
			UID:        remediationCR.GetUID(),
			APIVersion: remediationCR.GetAPIVersion(),
		},
		Started: remediationCR.GetCreationTimestamp(),
	}

	foundNode := false
	for _, unhealthyNode := range nhc.Status.UnhealthyNodes {
		if unhealthyNode.Name == node.Name {
			foundNode = true
			foundRem := false
			for _, rem := range unhealthyNode.Remediations {
				if rem.Resource.GroupVersionKind() == remediationCR.GroupVersionKind() {
					foundRem = true
					break
				}
			}
			if !foundRem {
				unhealthyNode.Remediations = append(unhealthyNode.Remediations, &remediation)
			}
			break
		}
		if foundNode {
			break
		}
	}
	if !foundNode {
		nhc.Status.UnhealthyNodes = append(nhc.Status.UnhealthyNodes, &remediationv1alpha1.UnhealthyNode{
			Name:         node.GetName(),
			Remediations: []*remediationv1alpha1.Remediation{&remediation},
		})
	}

}

func UpdateStatusNodeHealthy(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck) {
	delete(nhc.Status.InFlightRemediations, node.GetName())
	for i, _ := range nhc.Status.UnhealthyNodes {
		if nhc.Status.UnhealthyNodes[i].Name == node.GetName() {
			nhc.Status.UnhealthyNodes = append(nhc.Status.UnhealthyNodes[:i], nhc.Status.UnhealthyNodes[i+1:]...)
			break
		}
	}
}

func UpdateStatusNodeUnhealthy(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck) {
	foundNode := false
	for _, unhealthyNode := range nhc.Status.UnhealthyNodes {
		if unhealthyNode.Name == node.Name {
			foundNode = true
			break
		}

	}
	if !foundNode {
		nhc.Status.UnhealthyNodes = append(nhc.Status.UnhealthyNodes, &remediationv1alpha1.UnhealthyNode{
			Name: node.GetName(),
		})

	}
}

// FindStatusRemediation return the first remediation in the NHC's status for the given node which matches the remediationFilter
func FindStatusRemediation(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, remediationFilter func(r *remediationv1alpha1.Remediation) bool) *remediationv1alpha1.Remediation {
	for _, unhealthyNode := range nhc.Status.UnhealthyNodes {
		if unhealthyNode.Name == node.GetName() {
			for _, rem := range unhealthyNode.Remediations {
				if remediationFilter(rem) {
					return rem
				}
			}
		}
	}
	return nil
}
