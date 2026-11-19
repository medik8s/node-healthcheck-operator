package annotations

const (
	// NhcTimeOut is the annotation set by NHC to signal the operator that it surpassed its timeout and shall stop its remediation
	NhcTimedOut = "remediation.medik8s.io/nhc-timed-out"

	// MultipleTemplatesSupportedAnnotation is an annotation that indicates whether multiple templates of the same kind are supported by the template's remediator
	MultipleTemplatesSupportedAnnotation = "remediation.medik8s.io/multiple-templates-support"

	// NodeNameAnnotation is an annotation that contains the node name and is placed on CRs of remediators which supports multiple templates of the same kind.
	// It's required in order for the remediator to "know" which unhealthy node the CR represents .
	NodeNameAnnotation = "remediation.medik8s.io/node-name"

	// NodeUIDAnnotation contains the UID of the Node the remediation CR was created for.
	// A remediator can use it to tell that Node apart from a later Node registered under the same name.
	NodeUIDAnnotation = "remediation.medik8s.io/node-uid"

	// NodeSystemUUIDAnnotation contains the status.nodeInfo.systemUUID of the Node the remediation CR was created for.
	NodeSystemUUIDAnnotation = "remediation.medik8s.io/node-system-uuid"
)
