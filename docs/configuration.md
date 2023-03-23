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
  selector:
    matchExpressions:
      - key: node-role.kubernetes.io/control-plane
        operator: DoesNotExist
      - key: node-role.kubernetes.io/master
        operator: DoesNotExist
  remediationTemplate:
    apiVersion: self-node-remediation.medik8s.io/v1alpha1
    kind: SelfNodeRemediationTemplate
    namespace: <SNR namespace>
    name: self-node-remediation-resource-deletion-template
  escalatingRemediations:
    - remediationTemplate:
        apiVersion: superfast.medik8s.io/v1alpha1
        name: superfast
        namespace: openshift-operators
        kind: SuperFastRemediationTemplate
      order: 1
      timeout: 60s
    - remediationTemplate:
        apiVersion: normal.medik8s.io/v1alpha1
        name: normal
        namespace: openshift-operators
        kind: NormalRemediationTemplate
      order: 2
      timeout: 300s
  minHealthy: "51%"
  unhealthyConditions:
    - type: Ready
      status: "False"
      duration: 300s
    - type: Ready
      status: Unknown
      duration: 300s
```

### Spec Details

| Field                    | Mandatory                             | Default Value                                                                                   | Description                                                                                                                                                                                    |
|--------------------------|---------------------------------------|-------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _selector_               | yes                                   | n/a                                                                                             | A [LabelSelector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#resources-that-support-set-based-requirements) for selecting nodes to observe. See details below.  | 
| _remediationTemplate_    | yes but mutually exclusive with below | n/a                                                                                             | A [ObjectReference](https://kubernetes.io/docs/reference/kubernetes-api/common-definitions/object-reference/) to a remediation template provided by a remediation provider. See details below. |
| _escalatingRemediations_ | yes but mutually exclusive with above | n/a                                                                                             | A list of ObjectReferences to a remediation template with order and timeout. See details below.                                                                                                |
| _minHealthy_             | no                                    | 51%                                                                                             | The minimum number of healthy nodes selected by this CR for allowing further remediation. Percentage or absolute number.                                                                       |
| _pauseRequests_          | no                                    | n/a                                                                                             | A string list. See details below.                                                                                                                                                              |
| _unhealthyConditions_    | no                                    | `[{type: Ready, status: False, duration: 300s},{type: Ready, status: Unknown, duration: 300s}]` | List of UnhealthyCondition, which define node unhealthiness. See details below.                                                                                                                |

### Selector

The selector is selecting the nodes which should be observed. For its syntax have
a look at the [LabelSelector docs](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#resources-that-support-set-based-requirements)

A common selector for worker nodes looks like this:
```yaml
selector:
  matchExpressions:
    - key: node-role.kubernetes.io/control-plane
      operator: DoesNotExist
    - key: node-role.kubernetes.io/master
      operator: DoesNotExist
```
The reason for excluding `master` and `control-plane` nodes instead of selecting
`worker` nodes is, that this also prevents potentially unwanted remediation of
control plane nodes in compact clusters, where nodes have both roles.

For remediating control plane nodes use this:
```yaml
selector:
  matchExpressions:
    - key: node-role.kubernetes.io/control-plane
      operator: Exists
```
> **Note**
>
> On older clusters you have to use `master` in above example. You can't use
> both `master` and `control-plane` because the expressions are evaluated with
> logical "AND".

> **Warning**
>
> - Having a configuration which selects both worker and control plane nodes
    > is strongly discouraged, because control plane nodes have special handling
    > in NHC and potentially in remediators!
> - Multiple configurations must not select an overlapping node set! This can lead to unwanted remediations.

### RemediationTemplate

The remediation template is an [ObjectReference](https://kubernetes.io/docs/reference/kubernetes-api/common-definitions/object-reference/)
to a remediation template provided by a remediation provider. Mandatory fields
are `apiVersion`, `kind`, `name` and `namespace`.

> **Note**
> 
> This field is mutually exclusive with spec.EscalatingRemediations

Note that some remediators work with the template being created in any namespace,
others require it to be in their installation namespace.

Also, some remediators install a remediation template by default, which can be
used by NHC. This is e.g. the case for the SelfNodeRemediation remediator. The
example CR above shows how to use its "resource deletion strategy" template.

For other remediators you might need to create a template manually. Please check
their documentation for details.

For more details on the remediation template, and the remediation CRs created
by NHC based on the template, see [below](#remediation-resources)

### EscalatingRemediations

EscalatingRemediations is a list of RemediationTemplates with an order and
timeout field. Instead of just creating one remediation CR and waiting forever
that the node gets healthy, using this field offers the ability to to try
multiple remediaators one after another.
The `order` field determines the order in which the remediations are invoked
(lower order = earlier invocation). The `timeout` field determines when the
next remediation is invoked.

There are optional features available when using escalating remediations:
- when running into a timeout, NHC signals this to the remediator by adding
a "remediation.medik8s.io/nhc-timed-out" annotation to the remediation CR. The
remediator can use this to cancel its efforts.
- The other way around, when the remediator can't remediate the node for whatever
reason, or thinks it is done with everything, it can set a status condition of
type "Processing" with status "False" and a current "LastTransitionTime" on the
remediation CR. NHC will try the next remediator early then, in case the node
doesn't get healthy within a short period of time.

> **Note**
> 
> - This field is mutually exclusive with spec.RemediationTemplate
> - All other notes about remediation templates made above apply here as well

### UnhealthyConditions

This is a list of conditions for identifying unhealthy nodes. Each condition
has a mandatory type, status and duration.
Type and status are compared with the node's status conditions. When they match
for the time defined in duration, remediation will start. The list entries are
evaluated with a logical "OR".

Typically, the Ready condition is used, and the node is considered unhealthy when
the condition status is "False" (quoted because it needs to be string value)
or Unknown for some time. This also is the default value being set in case it's
empty when the CR is created:

```yaml
unhealthyConditions:
  - type: Ready
    status: "False"
    duration: 300s
  - type: Ready
    status: Unknown
    duration: 300s

```

> **Warning**
> 
> Be careful with the value of the duration field. While it's a common desire
> to remediate as quick as possible, a too low duration can trigger unneeded
> remediation (which typically means reboots) in case the cluster only has a
> short "hiccup", or when the node needs a longer time on start to get healthy.
> For finding the best value, you need to consider the hosts reboot time, the
> startup time of the kubernetes components and user workloads, and the
> downtime tolerance of the user workloads.

### PauseRequests

When pauseRequests has at least one value set, no new remediation will be
started, while in-flight remediations keep running.

It's recommended to use descriptive pause reasons like "performing cluster upgrade".

Updating pauseRequests on the command line works like this:

```shell
oc patch nhc/<name> --patch '{"spec":{"pauseRequests":["pause for cluster upgrade by @admin"]}}' --type=merge
```

## NodeHealthCheck Status

The status section of the NodeHealthCheck custom resource provides detailed
information about what the operator is doing. It contains these fields:

| Field                  | Description                                                                                                                                                                                                                                                |
|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _observedNodes_        | The number of nodes observed according to the selector.                                                                                                                                                                                                    |
| _healthyNodes_         | The number of observed healthy nodes.                                                                                                                                                                                                                      |
| _inFlightRemediations_ | ** DEPRECATED ** A list of "timestamp - node name" pairs of ongoing remediations. Replaced by unhealthyNodes.                                                                                                                                              |
| _unhealthyNodes_       | A list of unhealthy nodes and their remediations. See details below.                                                                                                                                                                                       |
| _conditions_           | A list of conditions representing NHC's current state. Currently the only used type is "Disabled", and it is true when the comtroller detects problems which prevent it to work correctly. See the [workflow page](./workflow.md) for further information. |
| _phase_                | A short human readable representation of NHC's current state. Known phases are Disabled, Paused, Remediating and Enabled.                                                                                                                                  |
| _reason_               | A longer human readable explanation of the phase.                                                                                                                                                                                                          |

### UnhealthyNodes

TODO add when documenting escalating remediation

## Remediation Resources

There are two kind of remediation resources involved:

- at least one remediation template CR, which needs to exist when configuring
a NHC CR.
- the remediation CRs created by NHC for unhealthy nodes, and being processed
by the remediator providing the related CRDs.

As mentioned above, the NHC CR is referencing one or more remediation templates.
While NHC does not know the template's CRD, it expects the CR to have a specific
structure, which looks like this:

```yaml
apiVersion: remediator.company.io/v1
kind: MyRemediationTemplate
metadata:
  name: test-name
  namespace: test-namespace
spec:
  template:
    spec: {}
```

> **Note**
> 
> - The kind must have a "Template" suffix.
> - The content of the inner spec doesn't matter. While it can be empty as in this
example, it has to exist though!

Here an example for a spec with more content:
```yaml
spec:
  template:
    spec:
      strategy: reboot
      timeout: 5m
```

When NHC detects an unhealthy node, it will create a CR based on this template,
following these steps:
- same apiVersion
- same kind but with stripped "Template" postfix
- same namespace
- name will be the unhealthy node's name
- spec will be a copy of spec.template.spec
- an owner reference will be set to the NHC CR
- another owner reference will be set the node's machine if available
(currently on OKD and OpenShift only)  

For the above template, a remediation CR will look like this:

```yaml
apiVersion: remediator.company.io/v1
kind: MyRemediation
metadata:
  name: unhealthy-node-name
  namespace: test-namespace
  ownerReferences:
    - kind: NodeHealthCheck
      apiVersion: remediation.medik8s.io/v1alpha1
      name: nhc-snr-worker
      uid: some-uid
spec:
  strategy: reboot
  timeout: 5m
```

### RBAC and role aggregation

In order to allow NHC to read template CRs, and to create/read/update/delete
remediation CRs, it's recommended to use role aggregation. For this the
remediator needs to create a ClusterRole with the needed rules, and label it
with `rbac.ext-remediation/aggregate-to-ext-remediation: "true"`.

Example: 

```yaml

apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  labels:
    rbac.ext-remediation/aggregate-to-ext-remediation: "true"
  name: my-aggregated-remediation-role
rules:
- apiGroups:
  - remediator.company.io/v1
  resources:
  - MyRemediationTemplate
  verbs:
  - get
- apiGroups:
  - remediator.company.io/v1
  resources:
  - MyRemediation
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
```