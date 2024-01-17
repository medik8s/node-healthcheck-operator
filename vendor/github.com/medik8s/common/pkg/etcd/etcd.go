// Package etcd is used for checking whether etcd disruption is allowd
//
// Important: add the following two lines in your project/code, so your RBAC will be updated with the right permissions
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
package etcd

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	etcdNamespace  = "openshift-etcd"
	errNoEtcdCheck = "can't check if the etcd quorum will be violated!"
)

// IsEtcdDisruptionAllowed checks if etcd disruption is allowed fot a given node
// It looks at the PodDisruptionBudget (PDB) of etcd quorum, and (if needed) search for the node's guard pod
func IsEtcdDisruptionAllowed(ctx context.Context, cl client.Client, log logr.Logger, node *corev1.Node) (bool, error) {
	// Check if new disruption is allowed
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := cl.List(ctx, pdbList, &client.ListOptions{Namespace: etcdNamespace}); err != nil {
		return false, err
	}
	if len(pdbList.Items) == 0 {
		log.Info(fmt.Sprintf("No PDBs were found, %s", errNoEtcdCheck), "namespace", etcdNamespace)
		return false, nil
	}
	if len(pdbList.Items) > 1 {
		log.Info(fmt.Sprintf("More than one PDB found, %s", errNoEtcdCheck), "namespace", etcdNamespace)
		return false, nil
	}
	pdb := pdbList.Items[0]
	if pdb.Status.DisruptionsAllowed >= 1 {
		log.Info("etcd disruption is allowed, thus node disruption is allowed", "Node", node.Name)
		return true, nil
	}

	log.Info("etcd PDB was found, but etcd disruption isn't allowed. Checking if node disruption will violate the etcd quorum", "Node", node.Name, "PDB", pdb.Name, "etcd allowed disruptions", pdb.Status.DisruptionsAllowed)

	// No etcd disruptions are allowed, but we still need to check if the given node will violate the etcd quorum
	// If it is already disrupted, then disrupting it again won't violate the etcd quorum. Otherwise, it would violate the etcd quorum
	// The PDB doesn't disclose which node is disrupted
	// So we have to check the etcd guard pods
	selector, err := metav1.LabelSelectorAsMap(pdb.Spec.Selector)
	if err != nil {
		log.Info(fmt.Sprintf("Could not parse PDB selector, %s", errNoEtcdCheck), "selector", pdb.Spec.Selector.String())
		return false, err
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
					log.Info("Node is already disrupted, thus disrupting it (again) won't violate the etcd quorum", "Node", node.Name, "Guard pod", pod.Name)
					return true, nil
				}
			}
			log.Info("Node is not disrupted, and disrupting it will violate the etcd quorum", "Node", node.Name, "Guard pod", pod.Name)
			return false, nil
		}
	}
	// Node is either already disrupted (and guard pod is missing) or it wasn't configured with etcd (to have a guard pod)
	log.Info("No guard pod was found for the node, and disrupting the node won't violate the etcd quorum,", "Node", node.Name)
	return true, nil
}
