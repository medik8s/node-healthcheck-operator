package utils

import (
	"context"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const etcdNamespace = "openshift-etcd"

// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// IsEtcdDisruptionAllowed checks if etcd disruption is allowed
func IsEtcdDisruptionAllowed(ctx context.Context, cl client.Client, log logr.Logger, node *corev1.Node) (bool, error) {
	log = log.WithName("etcd-pdb-checker")
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := cl.List(ctx, pdbList, &client.ListOptions{Namespace: etcdNamespace}); err != nil {
		return false, err
	}
	if len(pdbList.Items) == 0 {
		log.Info("No PDB found, can't check if etcd quorum will be violated! Refusing remediation!", "namespace", etcdNamespace)
		return false, nil
	}
	if len(pdbList.Items) > 1 {
		log.Info("More than one PDB found, can't check if etcd quorum will be violated! Refusing remediation!", "namespace", etcdNamespace)
		return false, nil
	}
	pdb := pdbList.Items[0]
	if pdb.Status.DisruptionsAllowed >= 1 {
		log.Info("Etcd disruption is allowed")
		return true, nil
	}

	// No disruptions allowed, so the only case we should remediate is that the node in question is already one of the disrupted ones
	// The PDB doesn't disclose which node is disrupted
	// So we have to check the etcd guard pods
	selector, err := metav1.LabelSelectorAsMap(pdb.Spec.Selector)
	if err != nil {
		log.Info("Could not parse PDB selector, can't check if etcd quorum will be violated! Refusing remediation!", "selector", pdb.Spec.Selector.String())
		return false, nil
	}
	podList := &corev1.PodList{}
	if err := cl.List(ctx, podList, &client.ListOptions{
		Namespace:     etcdNamespace,
		LabelSelector: labels.SelectorFromSet(selector),
	}); err != nil {
		return false, err
	}
	for _, pod := range podList.Items {
		if pod.Spec.NodeName == node.Name {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionFalse {
					log.Info("Etcd disruption not allowed, but the node is already disrupted, so remediation is allowed")
					return true, nil
				}
			}
			log.Info("Etcd disruption not allowed, but the node is already disrupted, so remediation is allowed")
			return true, nil
		}
	}

	log.Info("Etcd disruption is not allowed, and node is not already disrupted, so remediation is not allowed")
	return false, nil
}
