package nodes

import (
	v1 "k8s.io/api/core/v1"

	"github.com/medik8s/common/pkg/labels"
)

func IsControlPlane(node *v1.Node) bool {
	_, isControlPlane := node.Labels[labels.MasterRole]
	_, isMaster := node.Labels[labels.ControlPlaneRole]
	return isControlPlane || isMaster
}
