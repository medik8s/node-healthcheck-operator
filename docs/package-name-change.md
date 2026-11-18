# Package Name Change Migration Guide

## Summary

Beginning with version **5.9.0**, the Node Healthcheck Operator (NHC) community operator's OLM
package name changed from `node-healthcheck-operator` to **`medik8s-node-healthcheck-operator`**.

## Why this changed

On an OpenShift cluster, there are multiple catalogSources where each includes  
a different catalog with a set of OLM operators. The Medik8s operators have been
released to the Red Hat catalog and the [Community OKD](https://okd.io/) catalog 
using the same OLM package name. OLM and tooling such as `packagemanifests`
can't reliably distinguish between the operators' variants apart (each from a different
catalog), even if they share the same name, CRDs, etc. Prefixing the package name 
with `medik8s-` makes it unique, so it can always be unambiguously resolved to
our community operator regardless of which catalog source it's queried from.

## Breaking change

This is a **breaking change** for users of `node-healthcheck-operator` via OLM. 
Because OLM treats different package names as entirely separate operators, there is no automatic path from 
`node-healthcheck-operator` to `medik8s-node-healthcheck-operator` — the old operator must be uninstalled 
and the new one installed fresh.

**Note -** Before starting this migration, make sure there is no active remediation in progress. 
Doing this during remediation can leave a node in an inconsistent state.

## Migration steps

The examples below assume the operator is installed in the `openshift-workload-availability`
namespace. Adjust the namespace to match your installation.

### Step 1 — Remove the old Subscription

```bash
# Find the old Subscription (package: node-healthcheck-operator)
kubectl get subscription -n openshift-workload-availability

# Delete it (replace <subscription-name> with the name from the output above)
kubectl delete subscription <subscription-name> -n openshift-workload-availability
```

### Step 2 — Remove the old ClusterServiceVersion (CSV)

Deleting the Subscription does not remove the installed CSV. Delete the CSV whose name starts
with `node-healthcheck-operator.v...`:

```bash
# Find the old CSV
kubectl get csv -n openshift-workload-availability | grep node-healthcheck-operator

# Delete it (replace <csv-name>, e.g. node-healthcheck-operator.v5.8.0)
kubectl delete csv <csv-name> -n openshift-workload-availability
```

### Step 3 — Install the renamed package

**Option A — OpenShift Console:**
Go to **Operators → OperatorHub** (Software Catalog), search for
**"Node Health Check Operator"**, and open the community entry. Before installing, confirm the
package name shown is **`medik8s-node-healthcheck-operator`**, then install it into the
`openshift-workload-availability` namespace.

**Option B — CLI:**
Apply a new Subscription that references the renamed package:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: medik8s-node-healthcheck-operator
  namespace: openshift-workload-availability
spec:
  channel: stable
  name: medik8s-node-healthcheck-operator
  source: community-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
```

```bash
kubectl apply -f subscription.yaml
```

### Step 4 — Verify the installation

```bash
# The new CSV should be present and in the Succeeded phase
kubectl get csv -n openshift-workload-availability | grep medik8s-node-healthcheck-operator

# The controller pod should be Running
kubectl get pods -n openshift-workload-availability
```
