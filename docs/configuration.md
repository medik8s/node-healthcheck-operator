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
  remediationTemplate: # Note: mutually exclusive with escalatingRemediations
    apiVersion: self-node-remediation.medik8s.io/v1alpha1
    kind: SelfNodeRemediationTemplate
    namespace: <SNR namespace>
    name: self-node-remediation-automatic-strategy-template
  escalatingRemediations: # Note: mutually exclusive with remediationTemplate
    - remediationTemplate:
        apiVersion: self-node-remediation.medik8s.io/v1alpha1
        kind: SelfNodeRemediationTemplate
        namespace: <SNR namespace>
        name: self-node-remediation-automatic-strategy-template
      order: 1
      timeout: 300s
    # Note: The remediator below is an example only, it doesn't exist
    - remediationTemplate:
        apiVersion: reprovison.example.com/v1
        kind: ReprovisionRemediationTemplate
        namespace: example
        name: reprovision-remediation-template
      order: 2
      timeout: 30m
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

| Field                    | Mandatory                                           | Default Value                                                                                   | Description                                                                                                                                                                                                                                                                                                                                                     |
|--------------------------|-----------------------------------------------------|-------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _selector_               | yes                                                 | n/a                                                                                             | A [LabelSelector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#resources-that-support-set-based-requirements) for selecting nodes to observe. See details below.                                                                                                                                                                   |
| _remediationTemplate_    | yes but mutually exclusive with below               | n/a                                                                                             | A [ObjectReference](https://kubernetes.io/docs/reference/kubernetes-api/common-definitions/object-reference/) to a remediation template provided by a remediation provider. See details below.                                                                                                                                                                  |
| _escalatingRemediations_ | yes but mutually exclusive with above               | n/a                                                                                             | A list of ObjectReferences to a remediation template with order and timeout. See details below.                                                                                                                                                                                                                                                                 |
| _minHealthy_             | one of _minHealthy_ or _maxUnhealthy_ should be set | n/a                                                                                             | The minimum number of healthy nodes selected by this CR for allowing further remediation. Percentage or absolute number. _minHealthy_ and _maxUnhealthy_ are mutually exclusive.                                                                                                                                                                                |
| _maxUnhealthy_           | one of _minHealthy_ or _maxUnhealthy_ should be set | n/a                                                                                             | The maximum number of unhealthy nodes selected by this CR for allowing further remediation. Percentage or absolute number. _minHealthy_ and _maxUnhealthy_ are mutually exclusive. _maxUnhealthy_ should not be used with remediators that delete nodes (e.g. _MachineDeletionRemediation_), as this breaks the logic for counting healthy and unhealthy nodes. |
| _pauseRequests_          | no                                                  | n/a                                                                                             | A string list. See details below.                                                                                                                                                                                                                                                                                                                               |
| _unhealthyConditions_    | no                                                  | `[{type: Ready, status: False, duration: 300s},{type: Ready, status: Unknown, duration: 300s}]` | List of UnhealthyCondition, which defines node unhealthiness. See details below.                                                                                                                                                                                                                                                                                |
| _healthyDelay_           | no                                                  | 0                                                                                               | The time before NHC would allow a node to be healthy again. A negative value means that NHC will never consider the node healthy, and manual intervention is expected.                                                                                                                                                                                          |
| _stormTerminationDelay_  | no                                                  | n/a                                                                                             | When storm recovery regains the minHealthy/maxUnhealthy constraint, NHC waits this additional time before exiting storm mode. While waiting, no new remediations are created. Example: 30s, 2m, 1h.                                                                                                                                                             |

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
that the node gets healthy, using this field offers the ability to try
multiple remediators one after another.
The `order` field determines the order in which the remediations are invoked
(lower order = earlier invocation). The `timeout` field determines when the
next remediation is invoked.

There are optional features available when using escalating remediations:
- When running into a timeout, NHC signals this to the remediator by adding
a "remediation.medik8s.io/nhc-timed-out" annotation to the remediation CR. The
remediator can use this to cancel its efforts.
- The other way around, when the remediator fails to remediate the node, it can
set a status condition of type "Succeeded" with status "False" on the
remediation CR. NHC will try the next remediator without waiting for the
configured timeout to occur.

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
started, while ongoing remediations keep running.

It's recommended to use descriptive pause reasons like "performing cluster upgrade".

Updating pauseRequests on the command line works like this:

```shell
oc patch nhc/<name> --patch '{"spec":{"pauseRequests":["pause for cluster upgrade by @admin"]}}' --type=merge
```

### HealthyDelay
The healthyDelay field introduces a configurable delay before Node HealthCheck (NHC) will fully recognize a node as healthy again and remove its associated remediation.

Meaning and Motivation:

Historically, NHC would remove a remediation (and potentially allow taints to be removed) as soon as a node reported healthy. However, some customers require more precise control over this process, especially when dealing with nodes that might:

- "Flap" health status: Briefly regain health for a very short period only to become unhealthy again.
- Require post-remediation validation: Need a grace period to ensure stability or run specific checks before being fully integrated back into the cluster and having taints removed.

By configuring a healthyDelay, you can ensure that a node remains under observation for a specified duration after it first reports healthy. NHC will only delete its associated remediation (which in turn can trigger taint removal) once the node has maintained a healthy status for the entire configured delay.

Manual Intervention:

A negative value for healthyDelay has a special meaning: it tells NHC to never automatically consider the node healthy and to never automatically delete the remediation. This requires manual intervention to clear the remediation and allow the node to fully rejoin the cluster.

There are two methods for manual intervention:

- Method 1: Modifying the NodeHealthCheck CR (Affects all delayed nodes under this CR)

  - Action: Set the healthyDelay field to 0s (zero duration) or simply remove the field entirely from the NodeHealthCheck CR.
  - Impact: This action will affect all nodes currently being delayed by that specific NodeHealthCheck CR, allowing them to be considered healthy (if they meet other healthy conditions) and have their remediations removed without further delay. This is useful if you want to disable the delay for the entire set of nodes managed by this CR.

- Method 2: Using a Node Annotation (Node-specific intervention)

  - To trigger node-specific manual intervention, you should add the remediation.medik8s.io/manually-confirmed-healthy annotation (with any value) to the Node object of the node that you want to manually confirm as healthy.
  - When this annotation is present on a Node:
    - NHC will ignore the configured healthyDelay for that specific node.
    - NHC will update its internal status to reflect that the node is no longer being delayed by healthyDelay.
    - Once the node meets all other healthy criteria, NHC will delete the remediation.medik8s.io/manually-confirmed-healthy annotation from the Node, and proceed with deleting the remediation CR for that node.
    - This approach provides a precise, node-specific mechanism for an administrator to signal that a node is healthy and ready to exit the healthyDelay period, without affecting the healthyDelay configuration for other nodes under the same NodeHealthCheck CR.

### Storm Recovery

Storm recovery is an **optional advanced feature** that provides system stabilization through remediation restraint during mass failure scenarios.

**Real-World Example**:
```
Scenario: 20 nodes, minHealthy=11, stormTerminationDelay=30s

Normal operation: 15 healthy, 5 unhealthy  → 5 remediations created
5 additional nodes go down and storm hits: 10 healthy (<11) → storm recovery activates, new remediations are blocked (existing continue)
2 node regain health 12 healthy (≥11) → start 30s delay timer (3 remediations left, 8 unhealthy nodes) 
Exit: after 30s elapse → storm recovery deactivates, resume creating remediations for the 5 additional nodes 
```

#### When to Use Storm Recovery

**Storm Recovery is Appropriate When**:
- Your infrastructure can be overwhelmed by too many concurrent remediations
- You want controlled recovery during mass failure events
- You prefer "wait and see" over "aggressive remediation" during storms
- You accept that some failure scenarios are beyond automated recovery

**Storm Recovery is NOT Appropriate When**:
- You need aggressive remediation regardless of system load
- Your infrastructure can handle unlimited concurrent remediations
- You can't accept any remediation delays during mass failures

#### Configuring Storm Recovery

- Enable by setting `spec.stormTerminationDelay` on the `NodeHealthCheck` CR.
- Choose a delay that matches your environment's stabilization time. While the delay is counting down, new remediations remain blocked.
- If `stormTerminationDelay` is not set, storm recovery mode is disabled and only the standard `minHealthy`/`maxUnhealthy` gating applies.

#### Storm Recovery States

**State 1: Normal Operation**
```
healthyNodes ≥ minHealthy
→ Create remediations for unhealthy nodes (as allowed by minHealthy/maxUnhealthy)
```

**State 2: Storm Recovery Active**
```
healthyNodes < minHealthy
→ Block new remediations
```

**State 3: Regaining Health (Delay)**
```
healthyNodes ≥ minHealthy
→ Start stormTerminationDelay timer
→ Continue blocking new remediations until delay elapses
```

**State 4: Storm Recovery Exit**
```
after stormTerminationDelay
→ Exit storm recovery mode
→ Resume creating remediations
```


## NodeHealthCheck Status

The status section of the NodeHealthCheck custom resource provides detailed
information about what the operator is doing. It contains these fields:

| Field                       | Description                                                                                                                                                                                                                                                                                                                                                                               |
|-----------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _observedNodes_             | The number of nodes observed according to the selector.                                                                                                                                                                                                                                                                                                                                   |
| _healthyNodes_              | The number of observed healthy nodes.                                                                                                                                                                                                                                                                                                                                                     |
| _inFlightRemediations_      | ** DEPRECATED ** A list of "timestamp - node name" pairs of ongoing remediations. Replaced by unhealthyNodes.                                                                                                                                                                                                                                                                             |
| _unhealthyNodes_            | A list of unhealthy nodes and their remediations. See details below.                                                                                                                                                                                                                                                                                                                      |
| _conditions_                | A list of conditions representing NHC's current state. Currently using "Disabled" and "StormActive". "Disabled" is true when the controller detects problems which prevent it to work correctly. "StormActive" is true when a remediation storm is occuring (in case NHC is configured to opt in to storm recovery mode). See the [workflow page](./workflow.md) for further information. |
| _phase_                     | A short human readable representation of NHC's current state. Known phases are Disabled, Paused, Remediating and Enabled.                                                                                                                                                                                                                                                                 |
| _reason_                    | A longer human readable explanation of the phase.                                                                                                                                                                                                                                                                                                                                         |
| _stormTerminationStartTime_ | Timestamp when minHealthy/maxUnhealthy constraint was satisfied and the exit delay countdown started. Present while storm exit delay is in progress.                                                                                                                                                                                                                                      |

### UnhealthyNodes

The `unhealthyNodes` status field holds structured data for keeping track of
ongoing remediations. When a node recovered and is healthy again, the status
will be cleaned up.

This replaces the deprecated `inFlightRemediations` field.

An example:

```yaml
status:
  # skip other fields here...
  unhealthyNodes:
    - name: unhealthy-node-name
      remediations:
        - resource:
            apiVersion: self-node-remediation.medik8s.io/v1alpha1
            kind: SelfNodeRemediation
            namespace: <SNR namespace>
            name: unhealthy-node-name
            uid: abcd-1234...
          started: 2023-03-20T15:05:05Z01:00
          timedOut: 2023-03-20T15:10:05Z01:00 # timed out
        # when using `escalatingRemediations`, the next remediator will be appended:   
        - resource:
            apiVersion: reprovison.example.com/v1
            kind: ReprovisionRemediation
            namespace: example
            name: unhealthy-node-name
            uid: bcde-2345...
          started: 2023-03-20T15:10:07Z01:00
          # no timeout set: ongoing remediation
```

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
> - `kind` must have a "Template" suffix.
> - `spec` must contain the nested `template.spec` fields. The inner spec can be
> empty as in the above example, or have any content like here:

```yaml
spec:
  template:
    spec:
      strategy: reboot
      timeout: 5m
      extraParams:
        foo: bar
        importantNumber: 42
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