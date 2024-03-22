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
	caps := &Capabilities{}
	err := caps.discover(config)
	return *caps, err
}

func (c *Capabilities) discover(config *rest.Config) error {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return err
	}

	apiGroups, err := dc.ServerGroups()
	if err != nil {
		return err
	}

	ocpKind := schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
	machineAPIGroup := schema.GroupVersion{Group: "machine.openshift.io", Version: "v1"}

	for _, apiGroup := range apiGroups.Groups {
		for _, supportedVersion := range apiGroup.Versions {
			if supportedVersion.GroupVersion == ocpKind.GroupVersion().String() {
				c.IsOnOpenshift = true
				break
			}

			if supportedVersion.GroupVersion == machineAPIGroup.String() {
				c.HasMachineAPI = true
				break
			}
		}
	}
	return nil
}
