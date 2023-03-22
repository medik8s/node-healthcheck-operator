## NodeHealthCheck Custom Resource

The Node Healthcheck operator requires configuration by creating one or more
NHC custom resources. An example CR for remediating worker nodes with the
Self Node Remediation operator looks like this:

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nhc-snr-worker
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







### External Remediation Resources

External remediation resources are custom resource meant to be reconciled by specialized remediation providers.
The NHC object has a property of a Remediation Template, and this template Spec will be
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

- The template refers to a resource with the given properties. It is the admin's responsibility to ensure the relevant template resource exists.

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




`oc patch nhc/nhc-worker-default --patch '{"spec":{"pauseRequests":["pause for cluster upgrade by @admin"]}}' --type=merge`


### RBAC rules aggregation

Each provider must label it's rules with `rbac.ext-remediation/aggregate-to-ext-remediation: true` so the controller
will aggreate its rules and will have the proper permission to create/delete external remediation objects.

