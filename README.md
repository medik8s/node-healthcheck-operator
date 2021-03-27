# Under development

## Node Healthcheck Controller

A Node entering an unready state after 5 minutes is an obvious sign that a
failure occurred, but depending on your physical environment, workloads, and
tolerance for risk, there may be other criteria or thresholds that are
appropriate.

The [Node Healthcheck Controller]() checks each Node's set of [NodeConditions]()
against the criteria and thresholds defined for it in [NodeHealthCheck]() CRs.
If the Node is deemed to be in a failed state, and remediation is appropriate,
the controller will instantiate a RemediationRequest template (defined as part
of the CR) that specifies the mechansim/controller to be used for recovery.

Should the Node recover on its own, the NH controller removes the instantiated
RemediationRequest.  In all other respects, the RemediationRequest is owned by
the [target remediation mechanism]() and will persist until that controller is
satisfied remediation is complete.  For some mechanisms that may mean the Node
has entered a safe state (eg. the underlying "hardware" has been deprovisioned),
for others it may be the Node coming back online (eg. after a reboot).

Remediation is not always the correct response to a failure.  Especially in
larger clusters, we want to protect against failures that appear to take out
large portions of compute capacity but are really the result of failures on or
near the control plane.

For this reason, the healthcheck CR includes the ability to define a percentage
or total number of nodes that can be considered candidates for concurrent
remediation.

## NodeHealthCheck Custom Resource

Here is an example defintion of the resourcee

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
# mandatory
  externalRemediationTemplate:
    kind: ProviderXRemedyTemplate
    apiVersion: medik8s.io/v1alpha1
    name: group-x
    namespace: default
#  # optional
#  selector:
#    matchLabels:
#      kubernetes.io/os: linux
#    optionally use more fine grained matching
#    matchExpressions:
#      - key: another-node-label-key
#        operator: In
#        values:
#          - another-node-label-value
#
#  maxUnhealthy: "49%"
#  unhealthyConditions:
#    - type: Ready
#      status: False
#      duration: 300s

```

| Field | Mandatory | Default Value | Description |
| --- | --- | --- | --- |
| _externalRemediationTemplate_ | yes | n/a | A reference to a remediation template provided by an infrastructure provider. If a node needs remediation the controller will create an object from this template and then it should be picked up by a remediation provider.|
| _selector_ | no | empty selector that selects all nodes | a nodes selector of type [metav1.LabelSelector](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#LabelSelector) | 
| _maxUnhealthy_ | no | 49% | Any farther remediation is only allowed if at most "MaxUnhealthy" nodes selected by "selector" are not healthy.| 
| _unhealthyConditions_ | no | `[{type: Ready, status: False, duration: 300s}]` | list of the conditions that determine whether a node is considered unhealthy.  The conditions are combined in a logical OR, i.e. if any of the conditions is met, the node is unhealthy.|




### External Remediation API
TODO
