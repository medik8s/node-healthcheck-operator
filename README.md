# Node Healthcheck Operator

<p align="center">
<img width="200" src="config/assets/nhc_blue.png">
</p>

## Introduction

Hardware is imperfect, and software contains bugs. When node level failures such
as kernel freezes or dead NICs occur, the work required from the cluster does not
decrease - workloads from affected nodes need to be restarted somewhere.

However, some workloads, such as RWO volumes and StatefulSets, may require
at-most-one semantics.  Failures affecting these kind of workloads risk data
loss and/or corruption if nodes (and the workloads running on them) are assumed
to be dead whenever we stop hearing from them.  For this reason it is important
to know that the node has reached a safe state before initiating recovery of the
workload.

Unfortunately it is not always practical to require admin intervention in order
to confirm the node's true status. In order to automate the recovery of exclusive
workloads, the Medik8s project presents a collection of operators that can be installed on any
kubernetes-based cluster to automate failure detection and fencing / remediation.
For more information visit our [homepage](https://www.medik8s.io)

## Failure detection with the Node Healthcheck operator

### Handling unhealthy nodes

A Node entering an unready state after 5 minutes is an obvious sign that a
failure occurred. However, there may be other criteria or thresholds that are
more appropriate based on your particular physical environment, workloads,
and tolerance for risk.

The [Node Healthcheck operator](https://www.medik8s.io/failure_detection/#node-healthcheck-controller)
checks each Node's set of [NodeConditions](https://kubernetes.io/docs/concepts/architecture/nodes/#condition)
against the criteria and thresholds defined in NodeHealthCheck (NHC) custom
resources (CRs).

If the Node is deemed to be in a failed state, and remediation is appropriate,
the controller will instantiate a remediation custom resources based on the
remediation template(s) as defined in the NHC CR. NHC offers to configure
a single remediation method, or a list of remediation methods which will be
used one after another with specified order and timeout.

This template based mechanism allows cluster admins to use the best remediator
for their environment, without NHC having to know them beforehand. Remediators
might use e.g. Kubernetes' ClusterAPI, OpenShift's MachineAPI, BMC, Watchdog
or software based reboots for fencing the workloads.
For more details see the [remediation documentation](https://www.medik8s.io/remediation/remediation/).

When the Node recovers and gets healthy again, NHC will delete the
remediation CR for signalling that node recovery was successful.

### Special cases

#### Control plane problems

Remediation is not always the correct response to a failure. Especially in
larger clusters, we want to protect against failures that appear to take out
large portions of compute capacity but are really the result of failures on or
near the control plane. For this reason, the NHC CR includes the ability to
define a minimum number of healthy nodes, by percentage or absolute number.
When the cluster is falling short of this threshold, no further remediation
will be started.

#### Cluster Upgrades

Cluster upgrades usually draw workers reboots, mainly to apply OS updates.
These nodes might get unhealthy for some time during these reboots.
This disruption can also cause other nodes to overload and appear unhealthy,
when compensating for the lost compute capacity. Making remediation decisions
at this moment may interfere with the upgrade and may even fail it completely.
For that reason NHC will stop remediating new unhealthy nodes in case it
detects that a cluster is upgrading.

At the moment this is only supported on OpenShift, by monitoring the
[ClusterVersionOperator](https://github.com/openshift/cluster-version-operator).

#### Manual pausing

Before running cluster upgrades on kubernetes, or for any other reason, cluster
admins can prevent new remediation by pausing the NHC CR.

## Further information

For more details about using or contributing to Node Healthcheck, check out our
[docs](docs/readme.md).

## Help

Please join our [Google group](https://groups.google.com/g/medik8s) for asking
questions. When you find a bug, please open an issue in this repository.
