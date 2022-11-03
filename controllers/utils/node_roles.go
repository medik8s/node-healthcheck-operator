package utils

import v1 "k8s.io/api/core/v1"

const (
	// WorkerRoleLabel is the role label of worker nodes
	WorkerRoleLabel = "node-role.kubernetes.io/worker"
	// MasterRoleLabel is the old role label of control plane nodes
	MasterRoleLabel = "node-role.kubernetes.io/master"
	// ControlPlaneRoleLabel is the new role label of control plane nodes
	ControlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
)

func IsControlPlane(node *v1.Node) bool {
	_, isControlPlane := node.Labels[MasterRoleLabel]
	_, isMaster := node.Labels[ControlPlaneRoleLabel]
	return isControlPlane || isMaster
}
