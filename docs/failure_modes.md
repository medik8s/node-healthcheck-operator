# Node Health Check (NHC) — Failure modes

## Purpose

This document helps when **something is wrong with remediation behaviour**: NHC is **disabled**, **quiet** when you expect action, **stuck** on one step, or **refusing** spec changes. It stays **symptom-oriented**; the reconcile sequence lives in **`architecture.md`**.

## How problems show up

Each item below is: **what you notice** → **typical reason** → **where to look**.

1. **`NodeHealthCheck` reports `Disabled` and remediation never starts**  
   - **MHC conflict:** More than one **MachineHealthCheck**, or one that is not the allowed single **“termination handler”** pattern → NHC disables itself to avoid fighting OpenShift MHC.  
     *Look at:* `status.conditions` (reason **`ConflictingMachineHealthCheckDetected`**), cluster **MHC** count and their **`unhealthyConditions`**.  
   - **Template missing:** Referenced **remediation template** CR does not exist (yet) or API group not present → **`RemediationTemplateNotFound`**.  
     *Look at:* same conditions; operator often **requeues** on a short timer so a late-installed template can recover.  
   - **Template invalid:** Template exists but is not usable (e.g. broken **`spec.template`**, or **Metal3** template not in the expected namespace).  
     *Look at:* **`RemediationTemplateInvalid`** in conditions / events.

2. **Nodes look unhealthy but **no** new remediation CR appears**  
   - **Cluster upgrade deferral (OpenShift):** Upgrade checker thinks an upgrade is in progress → NHC **postpones** new remediations and **requeues**.  
     *Look at:* events **remediation skipped** / upgrade messaging; **ClusterVersion** progressing; node **machineconfig** annotations on affected nodes.  
   - **Pause:** **`spec.pauseRequests`** is non-empty → **no new** remediations; in-flight ones continue.  
     *Look at:* `spec.pauseRequests`, `status.phase` **Paused**.  
   - **minHealthy / maxUnhealthy:** Too few nodes count as “healthy” for the selector → NHC **skips** starting more work.  
     *Look at:* `status.healthyNodes`, `status.observedNodes`, events about skipped remediation.  
   - **Storm recovery:** **`spec.stormCooldownDuration`** set and storm / **cooldown** active → **no new** remediations until the logic clears.  
     *Look at:* conditions **`StormActive`**, **`StormCooldownActive`**, events about storm / skipped creation.  
   - **Lease not obtained:** Another reconcile or holder still owns the **per-node / per-NHC** lease path → create skipped, **requeue**.  
     *Look at:* lease-related skip events, timing of concurrent changes.  
   - **Control plane guard:** Another **control-plane** remediation is already in flight for a **different** node, or **etcd disruption** is not allowed (OpenShift path).  
     *Look at:* remediation CRs with **`remediation.medik8s.io/isControlPlaneNode`**, events about skipping CP remediation / retry in ~1 minute.  
   - **Node excluded:** Node has **`remediation.medik8s.io/exclude-from-remediation`**.  
     *Look at:* node **labels**.

3. **Escalating remediation does not move to the “next” template**  
   - **Current step still active:** Until the step **times out** or the remediation CR shows **failure** (`Succeeded=False`) before success, NHC keeps the **same** step.  
     *Look at:* remediation CR **conditions**, **`status.unhealthyNodes[].remediations`** ( **`timedOut`** not set yet).  
   - **Timeout / failure not processed yet:** Next reconcile needed after **`nhc-timed-out`** annotation and **`timedOut`** in status.  
     *Look at:* annotation **`remediation.medik8s.io/nhc-timed-out`** on the CR, NHC status entry **`timedOut`**.  
   - **No more steps:** All escalation steps have **timed out** → **no template left**; NHC stops escalating for that node (event such as **no template left**).  
     *Look at:* events, escalating list **order** / **timeout** values.

4. **Node conditions look healthy but remediation CR still exists (or user thinks node is “stuck unhealthy”)**  
   - **Deletion in progress:** NHC may still treat the node as tied to remediation until CRs **finish deleting** (finalizers). There is logic that logs when conditions are healthy but CRs are **pending deletion** for more than a short window.  
     *Look at:* CR **`metadata.deletionTimestamp`**, remediation **finalizers**, NHC **`unhealthyNodes`** and **conditionsHealthyTimestamp**-style bookkeeping in status.

5. **Orphan remediation CR after node renamed / removed**  
   - **Node no longer in the selected set** but CR remains → **orphan cleanup** runs; if the remediator expects **permanent node deletion**, cleanup may wait for **Succeeded** before deleting the CR.  
     *Look at:* remediation **conditions** (**Permanent node deletion expected** pattern from shared medik8s condition types), node existence vs selector.

6. **Cannot change `NodeHealthCheck` spec or delete it**  
   - **Validating webhook:** While **`unhealthyNodes`** still lists **active remediations** (non-empty **remediations**), **selector**, **remediation template**, and **escalating remediations** updates are **rejected**, and **delete** is **rejected**.  
     *Look at:* admission webhook denial message referencing **ongoing remediation**.

7. **OpenShift: node has `Terminating` condition**  
   - In the **single termination-handler MHC** mode, NHC **ignores** that node for unhealthy matching (handled by MHC).  
     *Look at:* MHC checker mode, node **conditions**.

8. **Healthy state or remediation cleanup doesn’t match expectations (`healthyDelay`)**  
   - **`spec.healthyDelay`** delays considering a node healthy again after conditions no longer match **`unhealthyConditions`**; remediation CRs may carry related annotations (e.g. **`remediation.medik8s.io/healthy-delay`**). A **negative** **`healthyDelay`** means the node is **not** auto-treated as healthy—expect **manual** steps.  
     *Look at:* `spec.healthyDelay`, remediation CR **annotations**, NHC **`status.unhealthyNodes[].healthyDelayed`** when present.

9. **`NodeHealthCheck` create or update rejected by admission**  
   - **`maxUnhealthy`** together with **`MachineDeletionRemediationTemplate`** (single template or any **escalating** entry) is **invalid**—the webhook / validation returns an error.  
     *Look at:* admission **denial message**, switch to **`minHealthy`** or change remediation template kind.

10. **Very old remediation CR**  
   - After a long lifetime (~**48 hours** in code), NHC may **flag** the CR for alerting (annotation / metrics) so operators notice **stuck** remediations.  
     *Look at:* CR age, annotation **`nodehealthcheck.medik8s.io/old-remediation-cr-flag`**, metrics.

## Important details

- **Requeue hints (not exhaustive):** template-not-found path uses a **short** requeue; upgrade deferral uses on the order of **~1 minute**; some skip paths requeue **~1 minute** (e.g. control-plane retry). Exact values are in the operator source (`nodehealthcheck_controller.go` constants).  
- **Annotation:** **`remediation.medik8s.io/nhc-timed-out`** — set on the **remediation** CR when NHC ends an **escalation step** (timeout or failure before success) so the remediator and the next step can align.  
- **This doc is not** a dump of every **event reason** string; use **`kubectl describe`** and operator logs when the above pointers are not enough.

## Related pieces

- **`architecture.md`** — order of gates (upgrade, pause, storm, minHealthy, leases, CP).  
- **`runbook.md`** — commands and field checklist.

## Scope

Does **not** replace vendor runbooks for **specific remediators** (SNR, MDR, …). Does **not** document every **metric** name.
