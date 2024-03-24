package annotations

const (
	// NhcTimeOut is the annotation set by NHC to signal the operator that it surpassed its timeout and shall stop its remediation
	NhcTimedOut = "remediation.medik8s.io/nhc-timed-out"

	// MultipleTemplatesSupportedAnnotation is an annotation that indicates whether multiple templates of the same kind are supported by the template's remediator
	MultipleTemplatesSupportedAnnotation = "remediation.medik8s.io/multiple-templates-support"

	// NodeNameAnnotation is an annotation that contains the node name and is placed on CRs of remediators which supports multiple templates of the same kind.
	// It's required in order for the remediator to "know" which unhealthy node the CR represents .
	NodeNameAnnotation = "remediation.medik8s.io/node-name"
)
