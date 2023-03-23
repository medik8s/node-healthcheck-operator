## NodeHealthCheck workflow

This is an overview about NHC's life cycle and workflow. This might be helpful
when debugging issues, since many steps can potentially fail. See also our
[troubleshooting guide](./troubleshooting.md).

### On NHC start

- various internal components are initialized
- RBAC configuration is updated for [role aggregation with remediators](./contributing.md#rbac-and-role-aggregation)
- old default configs are updated if found:
  - update selector to !control-plane && !master
  - update remediator to SelfNodeRemediation
  - **Note** due to the increasing number of potential configurations, the latest
    version of NHC is not creating a default config CR anymore
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
  - if it's a control plane node, and there are is an ongoing remediation for another control plane nodes, remediation is skipped for that node
  - if a remediation CR already exists, and it is older than 48 hours, a Prometheus metric is increased, which can be used for triggering alert
  - if no remediation CR exits yet, NHC will create it

### Remediation providers responsibility

It is up to the remediation provider to delete the remediation CR in case the
node is deleted, and a new one (with a different name) is reprovisioned.
In that specific scenario the NHC controller can not detect a successful node
remediation and delete the remediation CR itself.
