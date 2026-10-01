# Node Health Check (NHC) — Overview

## Purpose

The **Node Health Check operator** runs in the cluster and watches **Nodes** that match a **`NodeHealthCheck`** custom resource. When a node stays in a bad **Node condition** long enough, NHC can **create remediation objects** from templates you configure. Those objects are picked up by **remediation operators** (for example Self Node Remediation), which actually perform reboots, deletes, or other recovery actions.

NHC is the **orchestrator**: it decides **when** to start remediation and **which template** (or which step in a chain of templates) to use. It is **not** the component that powers off a machine or runs the watchdog.

## How it works

1. You define a **cluster-scoped** **`NodeHealthCheck`** with a **label selector** for which nodes are in scope.
2. You define **unhealthy rules** as a list of **condition type**, **condition status**, and **how long** that state must hold. If **any** rule matches, the node counts as unhealthy for that check (logical **OR** between rules).
3. You cap risk with either **minHealthy** or **maxUnhealthy** (you set **one** of them). That limits how many nodes can be remediated when many are unhealthy at once. **`maxUnhealthy`** cannot be used with **MachineDeletionRemediation**-style templates (by validation / webhook).
4. You choose **how** remediation is specified:
   - **Single template:** set **`remediationTemplate`** — one remediator template for every remediation this NHC starts, or
   - **Escalating remediation:** set **`escalatingRemediations`** — an **ordered** list of templates; each entry has an **order** and a **timeout**. NHC uses the first step, waits up to that timeout for the node to become healthy, then can advance to the next step if the current one times out or fails. You configure **either** a single template **or** escalating remediations, **not** both.
5. Optionally set **`healthyDelay`** to control how long NHC waits before treating a node as healthy again after conditions improve; a **negative** value means **no** automatic return to healthy—operators must intervene.
6. NHC records what it did in **`status`** (counts, per-node remediation tracking, and conditions such as **Disabled** or storm-related states).

## Important details

- **Remediation CRs** are normal Kubernetes objects; NHC creates them so **another operator** must reconcile them.
- **Escalating remediation:** Each step’s **timeout** is how long NHC waits before treating that step as finished unsuccessfully and moving on. The validating webhook requires every step’s timeout to be **at least one minute**.
- **Pausing:** **`pauseRequests`** stops **new** remediations; work already started continues.
- **Safety:** OpenShift-specific logic can **postpone** remediation during **cluster upgrade**, **limit control-plane** concurrency, and interact with **MachineHealthCheck** (for example disabling NHC if that would conflict, or ignoring **Terminating** nodes when a special single-MHC setup exists).
- **API group** for the main CR: **`remediation.medik8s.io`**, version **`v1alpha1`**, kind **`NodeHealthCheck`**, short name **`nhc`**.

## Related pieces

- **Remediators** (SNR, FAR, MDR, etc.) consume the CRs NHC creates; they report success/failure via **conditions** NHC watches.
- **Annotation** **`remediation.medik8s.io/nhc-timed-out`** on a remediation CR is set when NHC gives up on a **timed escalation step** (or similar timeout path) so the remediator can align cleanup and the next step can run.
- **Label** **`remediation.medik8s.io/exclude-from-remediation`** on a **Node** makes NHC skip that node.

## What this file is not

This overview does **not** describe reconcile ordering, leases, webhook field lists, or storm math. Those belong in **architecture** and **failure modes** so this file stays easy to read end-to-end.
