package cluster

import (
	"context"

	"github.com/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	v1 "github.com/openshift/api/config/v1"
	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

type Capabilities struct {
	HasEtcdPDBQuorum, HasMachineAPI bool
}

// HasMachineAPICapability returns true if the cluster has the MachineAPI Enabled
func HasMachineAPICapability(config *rest.Config) (bool, error) {
	configV1Client, err := configv1.NewForConfig(config)
	if err != nil {
		return false, errors.Wrapf(err, "failed to create cluster version client")
	}
	cvs, err := configV1Client.ClusterVersions().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "failed to get ClusterVersion")
	}

	for _, cv := range cvs.Items {
		if cv.Status.Capabilities.EnabledCapabilities == nil {
			return false, nil
		}
		var MachineAPI v1.ClusterVersionCapability = "MachineAPI"
		for _, capability := range cv.Status.Capabilities.EnabledCapabilities {
			if capability == MachineAPI {
				return true, nil
			}
		}
	}
	return false, nil
}
