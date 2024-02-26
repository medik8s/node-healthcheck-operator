package labels

const (
	// WorkerRole is the role label of worker nodes
	WorkerRole = "node-role.kubernetes.io/worker"
	// MasterRole is the old role label of control plane nodes
	MasterRole = "node-role.kubernetes.io/master"
	// ControlPlaneRole is the new role label of control plane nodes
	ControlPlaneRole = "node-role.kubernetes.io/control-plane"
	// DefaultTemplate label indicates to third party tools (e.g. UI) the default remediation template in case several exists.
	DefaultTemplate = "remediation.medik8s.io/default-template"
	// ExcludeFromRemediation label would be put on a node with value "true" in order to indicate this node should not be remediated.
	ExcludeFromRemediation = "remediation.medik8s.io/exclude-from-remediation"
)
