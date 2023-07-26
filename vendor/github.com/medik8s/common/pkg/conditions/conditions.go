package conditions

const (

	// These are condition types used on remediation CRs

	// ProcessingType is the condition type used to signal the remediation has started and it is in progress, or has finished
	ProcessingType = "Processing"
	// SucceededType is the condition type used to signal whether the remediation was successful or not
	SucceededType = "Succeeded"
	// PermanentNodeDeletionExpectedType is the condition type used to signal that the unhealthy node will be permanently deleted.
	PermanentNodeDeletionExpectedType = "PermanentNodeDeletionExpected"
)
