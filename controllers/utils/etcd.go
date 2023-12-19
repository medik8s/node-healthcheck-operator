package utils

import (
	"context"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const etcdNamespace = "openshift-etcd"

// +kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch

// IsEtcdDisruptionAllowed checks if etcd disruption is allowed
func IsEtcdDisruptionAllowed(ctx context.Context, cl client.Client, log logr.Logger) (bool, error) {
	log = log.WithName("etcd-pdb-checker")
	pdbList := &v1.PodDisruptionBudgetList{}
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
	log.Info("Etcd disruption is not allowed")
	return false, nil
}
