package cluster

import (
	"context"

	"github.com/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	v1 "github.com/openshift/api/config/v1"
	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

type Capabilities struct {
	IsOnOpenshift, HasMachineAPI bool
}

func NewCapabilities(config *rest.Config) (Capabilities, error) {
	var err error
	var onOpenshift, machineAPI bool

	if onOpenshift, err = isOnOpenshift(config); err != nil {
		return Capabilities{}, errors.Wrap(err, "failed to check if cluster is on OpenShift")
	} else if onOpenshift {
		if machineAPI, err = hasMachineAPICapability(config); err != nil {
			return Capabilities{}, errors.Wrap(err, "failed to check machine API capability")
		}
	}

	return Capabilities{
		IsOnOpenshift: onOpenshift,
		HasMachineAPI: machineAPI,
	}, nil
}

// IsOnOpenshift returns true if the cluster has the openshift config group
func isOnOpenshift(config *rest.Config) (bool, error) {
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

// HasMachineAPICapability returns true if the cluster has the MachineAPI Enabled
func hasMachineAPICapability(config *rest.Config) (bool, error) {
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
