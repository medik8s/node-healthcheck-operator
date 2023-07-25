package labels

const (
	// WorkerRole is the role label of worker nodes
	WorkerRole = "node-role.kubernetes.io/worker"
	// MasterRole is the old role label of control plane nodes
	MasterRole = "node-role.kubernetes.io/master"
	// ControlPlaneRole is the new role label of control plane nodes
	ControlPlaneRole = "node-role.kubernetes.io/control-plane"
	// ProcessingConditionType is the condition type used to signal the remediation has started and it is in progress, or has finished
	ProcessingConditionType = "Processing"
	// SucceededConditionType is the condition type used to signal whether the remediation was successful or not
	SucceededConditionType = "Succeeded"
)
