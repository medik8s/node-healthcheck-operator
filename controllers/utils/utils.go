package utils

import (
	"fmt"
	v1 "k8s.io/api/core/v1"
	"os"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// GetDeploymentNamespace returns the Namespace this operator is deployed on.
func GetDeploymentNamespace() (string, error) {
	// deployNamespaceEnvVar is the constant for env variable DEPLOYMENT_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	var deployNamespaceEnvVar = "DEPLOYMENT_NAMESPACE"

	ns, found := os.LookupEnv(deployNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", deployNamespaceEnvVar)
	}
	return ns, nil
}

// IsOnOpenshift returns true if the cluster has the openshift config group
func IsOnOpenshift(config *rest.Config) (bool, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return false, err
	}
	apiGroups, err := dc.ServerGroups()
	kind := schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
	for _, apiGroup := range apiGroups.Groups {
		for _, supportedVersion := range apiGroup.Versions {
			if supportedVersion.GroupVersion == kind.GroupVersion().String() {
				return true, nil
			}
		}
	}
	return false, nil
}

// GetLogWithNHC return a logger with NHC namespace and name
func GetLogWithNHC(log logr.Logger, nhc *v1alpha1.NodeHealthCheck) logr.Logger {
	return log.WithValues("NodeHealthCheck name", nhc.Name)
}

// MinRequeueDuration returns the minimal valid requeue duration
func MinRequeueDuration(old, new *time.Duration) *time.Duration {
	if new == nil || *new == 0 {
		return old
	}
	if old == nil || *old == 0 || *new < *old {
		return new
	}
	return old
}

func GetAllRemediationTemplates(nhc *v1alpha1.NodeHealthCheck) []*v1.ObjectReference {
	refs := make([]*v1.ObjectReference, 1)
	if nhc.Spec.RemediationTemplate != nil {
		refs = append(refs, nhc.Spec.RemediationTemplate)
	}
	for _, rem := range nhc.Spec.EscalatingRemediations {
		refs = append(refs, &rem.RemediationTemplate)
	}
	return refs
}
