package utils

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var (
	// DefaultRemediationDuration is used for node lease calculations for remediations without configured timeout
	DefaultRemediationDuration = 10 * time.Minute
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

// GetAllRemediationTemplates returns a slice of all ObjectReferences used as RemedediationTemplate in the
// given NodeHealthCheck
func GetAllRemediationTemplates(nhc *v1alpha1.NodeHealthCheck) []*v1.ObjectReference {
	if nhc.Spec.RemediationTemplate != nil {
		return []*v1.ObjectReference{nhc.Spec.RemediationTemplate}
	}
	refs := make([]*v1.ObjectReference, len(nhc.Spec.EscalatingRemediations))
	for i, rem := range nhc.Spec.EscalatingRemediations {
		refs[i] = &rem.RemediationTemplate
	}
	return refs
}

// GetRemediationDuration returns the expected remediation duration for the given CR, and all previous used templates
func GetRemediationDuration(nhc *v1alpha1.NodeHealthCheck, remediationCR *unstructured.Unstructured) (currentRemediationDuration, previousRemediationsDuration time.Duration) {

	if len(nhc.Spec.EscalatingRemediations) == 0 {
		return DefaultRemediationDuration, 0
	}

	// find current remediation
	var currentRemediation *v1alpha1.EscalatingRemediation
	for _, remediation := range nhc.Spec.EscalatingRemediations {
		if strings.TrimSuffix(remediation.RemediationTemplate.Kind, "Template") == remediationCR.GetKind() {
			currentRemediation = &remediation
			break
		}
	}

	if currentRemediation == nil {
		// should not happen...
		return DefaultRemediationDuration, 0
	}

	// get the timeout of the current escalating remediation for currentRemediationDuration
	currentRemediationDuration = currentRemediation.Timeout.Duration

	// get the sum of timeouts of all previous escalating remediations for previousRemediationsDuration
	for _, remediation := range nhc.Spec.EscalatingRemediations {
		if currentRemediation.Order > remediation.Order {
			previousRemediationsDuration += remediation.Timeout.Duration
		}
	}

	return
}
