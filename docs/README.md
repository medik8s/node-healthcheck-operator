## Node Healthcheck Controller
<p align="center">
<img width="200" src="config/assets/nhc_blue.png">
</p>


A Node entering an unready state after 5 minutes is an obvious sign that a
failure occurred, but depending on your physical environment, workloads, and
tolerance for risk, there may be other criteria or thresholds that are
appropriate.

The [Node Healthcheck Controller](https://www.medik8s.io/failure_detection/#node-healthcheck-controller) checks each Node's set of [NodeConditions](https://kubernetes.io/docs/concepts/architecture/nodes/#condition)
against the criteria and thresholds defined for it in [NodeHealthCheck](#nodehealthcheck-custom-resource) CRs.
If the Node is deemed to be in a failed state, and remediation is appropriate,
the controller will instantiate a RemediationRequest template (defined as part
of the CR) that specifies the mechansim/controller to be used for recovery.

Should the Node recover on its own, the NH controller removes the instantiated
RemediationRequest.  In all other respects, the RemediationRequest is owned by
the [target remediation mechanism](https://www.medik8s.io/remediation/remediation/) and will persist until that controller is
satisfied remediation is complete.  For some mechanisms that may mean the Node
has entered a safe state (eg. the underlying "hardware" has been deprovisioned),
for others it may be the Node coming back online (eg. after a reboot).

Remediation is not always the correct response to a failure.  Especially in
larger clusters, we want to protect against failures that appear to take out
large portions of compute capacity but are really the result of failures on or
near the control plane.

For this reason, the [healthcheck CR](#nodehealthcheck-custom-resource) includes
the ability to define a percentage or total number of nodes that can be
considered candidates for concurrent remediation.

When the controller starts it will create a default [healthcheck CR](#nodehealthcheck-custom-resource),
if there is no healthcheck CR at all in the cluster already(supporting upgrades
with existing configurations). The default CR works with the [self-node-remediation](https://github.com/medik8s/self-node-remediation), that
should be installed automatically if you deployed using the [operator hub].
The CR uses all defaults except a selector to select only worker nodes.

## Cluster Upgrade awareness

Cluster upgrade usually draw workers reboot, mainly to apply OS updates, and this
disruption can cause other nodes to overload to compensate for the lost compute capacity,
and may even start to appear unhealthy. Making remediation decisions at this moment may
interfere with the course of the upgrade and may even fail it completely.
For that NHC will stop remediating new unhealthy nodes in case in detects that a cluster is upgrading.
At the moment only OpenShift is supported since [ClusterVersionOperator](https://github.com/openshift/cluster-version-operator)
is automatically managing the cluster upgrade state.

If running on k8s, where this feature isn't supported yet, you can pause
any new remediation using the resource `_pauseRequests_` spec field.

`oc patch nhc/nhc-worker-default --patch '{"spec":{"pauseRequests":["pause for cluster upgrade by @admin"]}}' --type=merge`

See the [healthcheck CR](#nodehealthcheck-custom-resource) for more details.

## Installation

Install the Node Healthcheck operator using [operator hub]. The installation
will also install the [self-node-remediation] operator as a default remediator.

For development environments you can run `make deploy deploy-snr`.
See the [Makefile](./../Makefile) for more variables.

On start the controller creates a default resource name `nhc-worker-default`,
that remediates using Self Node Remediation, and will check for worker-only heath issues.
See [NodeHealthCheck Custom Resource](#nodehealthcheck-custom-resource) for the default properties.
If there is an existing resource the controller will not create a default one.

## NodeHealthCheck Custom Resource

Here is the default NHC resource the operator creates on start:

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nhc-worker-default
spec:
  # mandatory
  remediationTemplate:
    apiVersion: self-node-remediation.medik8s.io/v1alpha1
    kind: SelfNodeRemediationTemplate
    namespace: <SNR namespace>
    name: self-node-remediation-resource-deletion-template
  # see k8s doc on selectors https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#resources-that-support-set-based-requirements
  selector:
    matchExpressions:
      - key: node-role.kubernetes.io/control-plane
        operator: DoesNotExist
      - key: node-role.kubernetes.io/master
        operator: DoesNotExist
  minHealthy: "51%"
  unhealthyConditions:
    - type: Ready
      status: "False"
      duration: 300s
    - type: Ready
      status: Unknown
      duration: 300s
```

| Field                 | Mandatory | Default Value                                                                                   | Description                                                                                                                                                                                                                        |
|-----------------------|-----------|-------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _remediationTemplate_ | yes       | n/a                                                                                             | A reference to a remediation template provided by an infrastructure provider. If a node needs remediation the controller will create an object from this template and then it should be picked up by a remediation provider.       |
| _selector_            | no        | empty selector that selects all nodes                                                           | a nodes selector of type [metav1.LabelSelector](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#LabelSelector)                                                                                                             | 
| _minHealthy_          | no        | 51%                                                                                             | Any further remediation is allowed if "MinHealthy" nodes selected by "selector" are healthy.                                                                                                                                       |
| _pauseRequests_       | no        | n/a                                                                                             | prevents any new remdiation from starting, while in-flight remediations keep running. Each entry is free form, and ideally represents the requested party reason for this pausing e.g "imaginary-cluster-upgrade-manager-operator" |
| _unhealthyConditions_ | no        | `[{type: Ready, status: False, duration: 300s},{type: Ready, status: Unknown, duration: 300s}]` | list of the conditions that determine whether a node is considered unhealthy.  The conditions are combined in a logical OR, i.e. if any of the conditions is met, the node is unhealthy.                                           |

## NodeHealthCheck Status

The status section of the NodeHealthCheck custom resource provides detailed information about what the controller is
doing. It has these fields:

| Field                  | Description                                                                                                                                                                                                                                            |
|------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _observedNodes_        | the number of nodes observed by using the NHC spec.selecor                                                                                                                                                                                             |
| _healthyNodes_         | the number of healthy nodes observed                                                                                                                                                                                                                   |
| _inFlightRemediations_ | the timestamp when remediation triggered per node                                                                                                                                                                                                      |
| _conditions_           | represents the observations of a NodeHealthCheck's current state. The only used type is "Disabled" and it is true in case of an invalid config, or when conflicting MachineHealthChecks exist. See the condition's reason and message for more details |
| _phase_                | represents the current phase of this Config. Known phases are Disabled, Paused, Remediating and Enabled                                                                                                                                                |
| _reason_               | explains the current phase in more detail                                                                                                                                                                                                              |


## NodeHealthCheck life-cycle

When a node is unhealthy:
  - sum up how many other nodes are unhealthy.
  - if the number of healthy nodes > minHealthy the controllers creates the external remediation object
  - the external remediation object has an OwnerReference on the NodeHeathCheck object
  - controller updates the NodeHealthCheck.Status

When a node turns healthy:
  - the controller deletes the external remediation object
  - the controller updates the NodeHealthCheck.Status 


### External Remediation Resources

External remediation resources are custom resource meant to be reconciled by speciallized remediation providers.
The NHC object has a property of a External Remediation Template, and this template Spec will be
copied over to the External Remediation Object Spec.
For example this example NHC has this template defined:

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
  remediationTemplate:
    apiVersion: self-node-remediation.medik8s.io/v1alpha1
    kind: SelfNodeRemediationTemplate
    namespace: default
    name: test-template
```

- For the default configuration this will work out of the box. For other remediators or configurations
  it is the admin's responsibility to ensure the relevant template resource exists. 

```yaml
apiVersion: self-node-remediation.medik8s.io/v1alpha1
kind: SelfNodeRemediationTemplate
metadata:
  name: test-template
  namespace: default
spec:
  template:
    spec: {}
```

- the controller will create an object with the kind `SelfNodeRemediation` (kind of the template with trimmed 'Template' postfix)
  and the object will have ownerReference set to the corresponding NHC object

```yaml
apiVersion: self-node-remediation.medik8s.io/v1alpha1
kind: SelfNodeRemediation
metadata:
  # named after the target node
  name: worker-0-21
  namespace: default
  ownerReferences:
    - kind: NodeHealthCheck
      apiVersion: remediation.medik8s.io/v1alpha1
      name: nodehealthcheck-sample
spec: {}

```

## Remediation Providers responsibility

  It is upto the remediation provider to delete the external remediation object if the node is deleted and another is
  reprovisioned. In that specific scenario the controller can not assume a successful node remediation because the
  node with that name doesn't exist, and instead there will be a new one.

### RBAC rules aggregation

Each provider must label it's rules with `rbac.ext-remediation/aggregate-to-ext-remediation: true` so the controller
will aggreate its rules and will have the proper permission to create/delete external remediation objects.

[operator hub]: https://operatorhub.io/operator/node-healthcheck-operator
[self-node-remediation]: https://github.com/medik8s/self-node-remediation

## Help

Feel free to join our google group to get more info - https://groups.google.com/g/medik8s