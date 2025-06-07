package cluster

import (
	"context"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
)

type Capabilities struct {
	IsOnOpenshift, HasMachineAPI, IsSupportedControlPlaneTopology bool
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch

// NewCapabilities returns a Capabilities struct with the following fields:
// - IsOnOpenshift: true if the cluster is an OpenShift cluster
// - HasMachineAPI: true if the cluster has the Machine API installed
// - IsSupportedControlPlaneTopology: true if the cluster has a supported control plane topology
func NewCapabilities(config *rest.Config, reader client.Reader, logger logr.Logger, ctx context.Context) (Capabilities, error) {
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

	// check API groups for OCP and MachineAPI groups
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

	// check topology, default to true on non-openshift clusters
	caps.IsSupportedControlPlaneTopology = true
	if caps.IsOnOpenshift {
		infra := &configv1.Infrastructure{}
		err = reader.Get(ctx, client.ObjectKey{Name: "cluster"}, infra)
		if err != nil {
			return Capabilities{}, err
		}
		cpTopology := infra.Status.ControlPlaneTopology

		// add new supported topology modes here if needed
		// don't allow new topologies by default, because they might implement their own fencing logic, which can conflict with this operator
		// see https://github.com/openshift/api/blob/c1fdeb0788c16659238d93ec45ce2e798c14eb88/config/v1/types_infrastructure.go#L129-L147
		if cpTopology == configv1.HighlyAvailableTopologyMode ||
			cpTopology == configv1.SingleReplicaTopologyMode ||
			// quick fix without updating openshift/api dependency, it would need too many other updates
			// see https://github.com/openshift/api/blob/017e9dd0276e4ed0242a759dffd419d728337876/config/v1/types_infrastructure.go#L142
			// update to const once deps are updated
			cpTopology == "HighlyAvailableArbiter" ||
			cpTopology == configv1.ExternalTopologyMode {

			logger.Info("supported control plane topology", "topology", cpTopology)
			caps.IsSupportedControlPlaneTopology = true

		} else {
			logger.Info("unsupported control plane topology", "topology", cpTopology)
			caps.IsSupportedControlPlaneTopology = false
		}
	}

	return caps, nil
}
