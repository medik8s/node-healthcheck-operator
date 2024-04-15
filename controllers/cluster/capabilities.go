package cluster

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

type Capabilities struct {
	IsOnOpenshift, HasMachineAPI bool
}

func NewCapabilities(config *rest.Config) (Capabilities, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return Capabilities{}, err
	}

	apiGroups, err := dc.ServerGroups()
	if err != nil {
		return Capabilities{}, err
	}

	ocpKind := schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
	machineAPIGroup := schema.GroupVersion{Group: "machine.openshift.io", Version: "v1"}

	caps := Capabilities{}
	for _, apiGroup := range apiGroups.Groups {
		for _, supportedVersion := range apiGroup.Versions {
			if supportedVersion.GroupVersion == ocpKind.GroupVersion().String() {
				caps.IsOnOpenshift = true
				break
			}

			if supportedVersion.GroupVersion == machineAPIGroup.String() {
				caps.HasMachineAPI = true
				break
			}
		}
	}
	return caps, nil
}
