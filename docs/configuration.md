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
| _stormRecoveryThreshold_ | no                                                  | n/a                                                                                             | The threshold for proceeding with remediation during storm scenarios. When storm mode is triggered by number of remediation reaching minHealthy/maxUnhealthy maximum, NHC waits for the unhealthy count to drop to this threshold before resuming normal remediation. See Storm Recovery section below.                                                         |

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

## Storm Recovery

Storm recovery is an **optional advanced feature** that provides system stabilization through remediation restraint during mass failure scenarios.

### Concept & Philosophy

**Core Principle**: Storm recovery is about **system stabilization through remediation restraint**.

**The Philosophy**:
- **Recognition**: When many nodes fail, creating more remediations may be counterproductive
- **Assumption**: The system needs time to stabilize before additional remediation attempts
- **Strategy**: Temporary restraint to prevent making a bad situation worse
- **Acceptance**: Some scenarios may be beyond automated recovery (same as minHealthy)

**Real-World Example**:
```
Scenario: 20 nodes, minHealthy=11, stormRecoveryThreshold=5

Normal operation: 15 healthy, 5 unhealthy → 5 remediations created
Storm hits: 11 healthy, 9 unhealthy → 9 remediations created (minHealthy limit reached)
Additional failures: 9 healthy, 11 unhealthy → 9 remediations continue, 2 nodes wait due to minHealthy constraint

Key insight: Those 2 additional failed nodes get NO remediation
- It's intentional restraint
- Assumption: At this stage creating more remediations might destabilize the system further
- Strategy: Wait for system to stabilize (unhealthy count ≤ 5) before creating remediations for the 2 additional failed nodes.
- Risk: If more nodes keep failing, storm mode continues indefinitely even if current remediations finish successfully
- Reality: In such cases, the cluster may be beyond automated help anyway
```

### How Storm Recovery Works

**The Key Insight**: Storm recovery provides a **controlled exit strategy** when maximum number of remediation is reached due to minHealthy/maxUnhealthy constraint.

**Normal minHealthy Behavior**:
```
If (Maximum number of remediation is reached):
  → Block creation of new remediations
  → Existing remediations continue
  → Wait until current remediations are below maximum before creating more (as defined by minHealthy/maxUnhealthy)
```

**Storm Recovery Enhancement**:
```
If (Maximum number of remediation is reached AND stormRecoveryThreshold configured):
  → Enter storm recovery mode  
  → Block creation of new remediations as long as in storm recovery mode 
  → BUT: Exit storm recovery when (unhealthyNodes ≤ stormRecoveryThreshold)
  → Resume normal remediation for remaining unhealthy nodes
```

### When to Use Storm Recovery

**Storm Recovery is Appropriate When**:
- Your infrastructure can be overwhelmed by too many concurrent remediations
- You want controlled recovery during mass failure events
- You prefer "wait and see" over "aggressive remediation" during storms
- You accept that some failure scenarios are beyond automated recovery

**Storm Recovery is NOT Appropriate When**:
- You need aggressive remediation regardless of system load
- Your infrastructure can handle unlimited concurrent remediations
- You can't accept any remediation delays during mass failures

### Calculating Thresholds

**Step-by-Step Guide**:
```
1. Choose minHealthy based on availability requirements (unchanged)
2. Consider: "At what point is it safe to resume creating new remediations ?"
3. Set stormRecoveryThreshold = acceptable unhealthy count for resumption
4. Validate: stormRecoveryThreshold < (totalNodes - minHealthy)

Example:
- 20 worker nodes
- minHealthy=11 (55% availability requirement)
- Can handle 5 concurrent remediations safely
- Choose: stormRecoveryThreshold=5
- Meaning: "After a 9 remediation storm, resume normal remediation only when ≤5 nodes need remediation"
```

### Storm Recovery States

**State 1: Normal Operation**
```
healthyNodes > minHealthy
→ Create remediations for unhealthy nodes (up to minHealthy limit)
```

**State 2: Storm Recovery Triggered**
```
healthyNodes <= minHealthy
→ Block new remediations until storm recovery exit
```

**State 3: Storm Recovery Active**  
```
unhealthyNodes > stormRecoveryThreshold
→ Block new remediations
→ Wait for existing remediations to become healthy and reduce unhealthy count
```

**State 4: Storm Recovery Exit**
```
unhealthyNodes ≤ stormRecoveryThreshold  
→ Exit storm recovery mode
→ Resume creating remediations for unhealthy nodes
```

**Example State Transitions**:
```
20 nodes, minHealthy=11, stormRecoveryThreshold=5

1. Storm hits: 11 healthy, 9 unhealthy → 9 remediations active
2. More failures: 9 healthy, 11 unhealthy → still 9 remediations, 2 nodes wait
3. System stabilizes: 15 healthy, 5 unhealthy (3 remediations remain) → storm recovery exits
4. Resume remediation: Create remediations for remaining 2 unhealthy nodes
5. Final recovery: All nodes healthy

Risk scenario:
1. Storm hits: 11 healthy, 9 unhealthy → 9 remediations active  
2. Continuous failures: 5 healthy, 15 unhealthy → still 9 remediations, 6 nodes wait
3. Storm never ends even if the 9 remediations will recover: System overwhelmed, storm recovery stays active
4. Outcome: Cluster beyond automated recovery (manual intervention needed)
```

### Storm Recovery Behavior

**What Storm Recovery Does**:
- ✅ **Preserves existing remediations** (let them complete)
- ❌ **Blocks new remediations** (even for newly failed nodes)
- ⏳ **Waits for system stabilization** (unhealthy count to drop)
- 🔄 **Resumes when safe** (unhealthy ≤ threshold)

**What Storm Recovery Does NOT Do**:
- ❌ Does NOT guarantee all nodes will be remediated
- ❌ Does NOT provide unlimited recovery capability

### Risk Assessment

**Accepted Risks**:
```
Risk: Storm recovery may never exit if failures continue
Reality: Such scenarios likely indicate cluster-wide catastrophic failure
Philosophy: Automated remediation isn't the solution for every failure mode
Mitigation: Manual intervention, cluster replacement, or disaster recovery procedures
```

**Risk vs. Benefit Analysis**:
```
Without Storm Recovery:
✅ All unhealthy nodes get remediations (until minHealthy limit)
❌ Risk of overwhelming system during mass failures
❌ Potential to make storms worse through remediation load

With Storm Recovery:
✅ Controlled system load during storms
✅ Natural stabilization opportunities
❌ Some nodes may not get remediations during extended storms
❌ Relies on manual intervention for extreme scenarios
```

### Configuration Examples

**Example 1: minHealthy (percentage)**:
```yaml
spec:
  minHealthy: "60%"           # 12 out of 20 nodes
  stormRecoveryThreshold: 3   # Resume when ≤3 unhealthy
# Philosophy: Percentage-based minHealthy scales for dynamic cluster sizes
# Trade-off: minHealthy scales with cluster growth/shrinkage automatically
```

**Example 2: maxUnhealthy (fixed number)**:
```yaml
spec:
  maxUnhealthy: 5             # Allow max 5 unhealthy nodes
  stormRecoveryThreshold: 3   # Resume when ≤3 unhealthy
# Philosophy: Fixed maxUnhealthy limit regardless of cluster size
# Trade-off: maxUnhealthy doesn't scale with cluster changes
```

**Example 3: minHealthy (fixed number)**:
```yaml
spec:
  minHealthy: 12              # Require exactly 12 healthy nodes
  stormRecoveryThreshold: 5   # Resume when ≤5 unhealthy
# Philosophy: Absolute minimum healthy node requirement
# Trade-off: minHealthy doesn't scale with cluster changes
```

## NodeHealthCheck Status

The status section of the NodeHealthCheck custom resource provides detailed
information about what the operator is doing. It contains these fields:

| Field                    | Description                                                                                                                                                                                                                                                |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| _observedNodes_          | The number of nodes observed according to the selector.                                                                                                                                                                                                    |
| _healthyNodes_           | The number of observed healthy nodes.                                                                                                                                                                                                                      |
| _inFlightRemediations_   | ** DEPRECATED ** A list of "timestamp - node name" pairs of ongoing remediations. Replaced by unhealthyNodes.                                                                                                                                              |
| _unhealthyNodes_         | A list of unhealthy nodes and their remediations. See details below.                                                                                                                                                                                       |
| _conditions_             | A list of conditions representing NHC's current state. Currently the only used type is "Disabled", and it is true when the controller detects problems which prevent it to work correctly. See the [workflow page](./workflow.md) for further information. |
| _phase_                  | A short human readable representation of NHC's current state. Known phases are Disabled, Paused, Remediating and Enabled.                                                                                                                                  |
| _reason_                 | A longer human readable explanation of the phase.                                                                                                                                                                                                          |
| _stormRecoveryActive_    | Boolean indicating if storm recovery mode is currently active. Present only when stormRecoveryThreshold is configured.                                                                                                                                     |
| _stormRecoveryStartTime_ | Timestamp when storm recovery mode was activated. Present only when stormRecoveryActive is true.                                                                                                                                                           |

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