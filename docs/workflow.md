## NodeHealthCheck workflow

This is an overview about NHC's life cycle and workflow. This might be helpful
when debugging issues, since many steps can potentially fail. See also our
[troubleshooting guide](./troubleshooting.md).

### On NHC start

- various internal components are initialized
- RBAC configuration is updated for [role aggregation with remediators](./contributing.md#rbac-and-role-aggregation)
- the UI plugin is configured (on OKD / OpenShift only)

### When a NHC CR is created / updated / deleted, or an observed node's status condition changes

> **Note**
> In general, most of these steps also result in an update of the NHC status,
> and many steps also emit events. We skip mentioning this below.

- The API server and NHC will validate CRs BEFORE the create / update / delete request is persisted
- Additional validations are running when the CR is processed, which potentially results in a disabled NHC:
  - MachineHealthChecks exists (on OKD / OpenShift only)
  - The referenced remediation templates don't exist or are malformed (see [expected structure](./configuration.md#remediation-resources))
- Processing also stops when
  - the cluster is upgrading (on OKD / OpenShift only)
  - the NHC CR has pauseRequests
- Potentially existing remediation CRs are deleted for healthy nodes
- Processing stops when minHealthy check fails
- Unhealthy nodes are remediated:
  - if it's a control plane node, and there are ongoing remediations for other control plane nodes, remediation is skipped for that node
  - when storm recovery is active (triggered when healthy nodes < minHealthy and stormTerminationDelay is configured), creation of new remediations is blocked
  - if a remediation CR already exists:
    - in all cases, when it is older than 48 hours, a Prometheus metric is increased, which can be used for triggering an alert
    - when using escalating remediations:
      - and the timeout occurred
      - or the processing condition was set + a short amount of time elapsed:
        - set a timeout annotation the old remediation CR
        - create a new remediation CR for the next remediator, if any is left
  - if no remediation CR exits yet, NHC will create it, using the top level remediation template, or the first escalating remediation respectively

### Remediation providers responsibility

It is up to the remediation provider to delete the remediation CR in case the
node is deleted, and a new one (with a different name) is reprovisioned.
In that specific scenario the NHC controller can not detect a successful node
remediation and delete the remediation CR itself.

### Special cases

#### Ignoring unhealthy node condition duration in case node condition changes during remediation

Sometimes the node status changes from one condition to another one during remediation, and both conditions are configured as unhealthy.
E.g. when a node with condition type `Ready` and status `Unknown` is rebooted, it typically moves to `Ready` = `False` during reboot,
before finally moving to `Ready` = `True` when done.

This can result in remediation loops: in theory the node has to be considered as healthy while `Ready` = `False`, as long as the configured duration
didn't expire yet. Subsequently, when the duration expires before the node moves to `Ready` = `True`, the node is considered unhealthy and remediated again.

In order to prevent such remediation loops, NHC will ignore unhealthy nodes which don't have any unhealthy condition with expired duration anymore,
but an unhealthy condition with not expired duration.

That means:

- remediation CRs are not deleted during that time
- for escalating remediations, NHC won't proceed to the next remediation

NHC will continue to handle the node as either unhealthy when the duration expires, or as healthy in case no unhealthy condition matches anymore.
