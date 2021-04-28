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
  remediationTemplate:
    kind: ProviderXRemediationTemplate
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
| _remediationTemplate_ | yes | n/a | A reference to a remediation template provided by an infrastructure provider. If a node needs remediation the controller will create an object from this template and then it should be picked up by a remediation provider.|
| _selector_ | no | empty selector that selects all nodes | a nodes selector of type [metav1.LabelSelector](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#LabelSelector) | 
| _maxUnhealthy_ | no | 49% | Any farther remediation is only allowed if at most "MaxUnhealthy" nodes selected by "selector" are not healthy.| 
| _unhealthyConditions_ | no | `[{type: Ready, status: False, duration: 300s}]` | list of the conditions that determine whether a node is considered unhealthy.  The conditions are combined in a logical OR, i.e. if any of the conditions is met, the node is unhealthy.|

## NodeHealthCheck life-cycle

- when a node is unhealthy
  - sum up how many other nodes are unhealthy.
  - if the number of unhealthy nodes < maxUnhealthy the controllers creates the external remediation object
  - the external remediation object has an OwnerReference on the NodeHeathCheck object
- controller updates the NodeHealthCheck.Status
- when a node turn healthy
  - node-healthcheck-controller deletes the external remediation object
  - controller updates the NodeHealthCheck.Status 


### External Remediation Resources

External remediation resources are custom resource meant to be reconciled by speciallized remediation providers.
The NHC object has a property of a External Remediaiton Template, and this template Spec will be
copied over to the External Remediation Object Spec.
For example this example NHC has this template defined:

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sameple
...
spec:
  remediationTemplate:
    kind: ProviderXRemediationTemplate
    apiVersion: medik8s.io/v1alpha1
    name: group-x
    namespace: default
...

```

- it is the admin's responsiblity to create a template object from the template kind `ProviderXRemdiationTemplate`
  with the name `group-x`.

```yaml
apiVersion: medik8s.io/v1alpha1
kind: ProviderXRemdiationTemplate
metadata:
  name: group-x
  namespace: default
spec:
  template:
    # whatever
    size: 42

```
- the controller will create an object with the kind `ProviderXRemdiation` (postfix 'Template' trimmed)
  and the object will have ownerReference set to the co-responding NHC object

```yaml
apiVersion: medik8s.io/v1alpha1
kind: ProviderXRemdiation
metadata:
  # named after the target node
  name: worker-0-21
  namespace: default
  ownerReferences:
    - kind: NodeHealthCheck
      apiVersion: medik8s.io/v1alpha1
      name: nodehealthcheck-sample
spec:
  size: 42

```

## Remediation Providers responsibility

  It is upto the remediation provider to delete the external remediation object if the node is deleted and another is
  reprovisioned. In that specific scenario the controller can not assume a successful node remediation because the
  node with that name doesn't exist, and instead there will be a new one.

### RBAC rules aggregation

Each provider must label it's rules with `rbac.ext-remediation/aggregate-to-ext-remediation: true` so the controller
will aggreate its rules and will have the proper permission to create/delete external remediation objects
  each 
