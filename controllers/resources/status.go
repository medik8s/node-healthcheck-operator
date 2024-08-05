package resources

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/metrics"
)

func UpdateStatusRemediationStarted(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, remediationCR *unstructured.Unstructured) {
	templateName := utils.GetTemplateNameFromCR(*remediationCR)
	remediation := remediationv1alpha1.Remediation{
		Resource: corev1.ObjectReference{
			Kind:       remediationCR.GetKind(),
			Namespace:  remediationCR.GetNamespace(),
			Name:       remediationCR.GetName(),
			UID:        remediationCR.GetUID(),
			APIVersion: remediationCR.GetAPIVersion(),
		},
		Started:      remediationCR.GetCreationTimestamp(),
		TemplateName: templateName,
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

func UpdateStatusNodeHealthy(nodeName string, nhc *remediationv1alpha1.NodeHealthCheck) {
	for i, _ := range nhc.Status.UnhealthyNodes {
		if nhc.Status.UnhealthyNodes[i].Name == nodeName {
			for _, remediation := range nhc.Status.UnhealthyNodes[i].Remediations {
				remediation := remediation
				remediationResource := remediation.Resource
				duration := time.Now().Sub(remediation.Started.Time)
				metrics.ObserveNodeHealthCheckRemediationDeleted(remediationResource.Name, remediationResource.Namespace, remediationResource.Kind)
				metrics.ObserveNodeHealthCheckUnhealthyNodeDuration(remediationResource.Name, remediationResource.Namespace, remediationResource.Kind, duration)
			}
			nhc.Status.UnhealthyNodes = append(nhc.Status.UnhealthyNodes[:i], nhc.Status.UnhealthyNodes[i+1:]...)
			break
		}
	}
}

func UpdateStatusNodeUnhealthy(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck) {
	for _, unhealthyNode := range nhc.Status.UnhealthyNodes {
		if unhealthyNode.Name == node.Name {
			return
		}
	}
	nhc.Status.UnhealthyNodes = append(nhc.Status.UnhealthyNodes, &remediationv1alpha1.UnhealthyNode{
		Name: node.GetName(),
	})
}

func UpdateStatusNodeConditionsHealthy(nodeName string, nhc *remediationv1alpha1.NodeHealthCheck, now time.Time) *time.Time {
	for i, _ := range nhc.Status.UnhealthyNodes {
		if nhc.Status.UnhealthyNodes[i].Name == nodeName {
			if nhc.Status.UnhealthyNodes[i].ConditionsHealthyTimestamp == nil {
				nhc.Status.UnhealthyNodes[i].ConditionsHealthyTimestamp = &metav1.Time{Time: now}
			}
			return &nhc.Status.UnhealthyNodes[i].ConditionsHealthyTimestamp.Time
		}
	}
	return nil
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
