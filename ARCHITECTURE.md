# Node Health Check (NHC) — Architecture

## Purpose

This document explains **how the Node Health Check operator is structured**: what runs inside the operator process, which reconcilers exist, and **in what order** major decisions happen when a **`NodeHealthCheck`** is reconciled. It is written for someone who already knows what NHC does at a product level (see **`overview.md`**).

## How it works

1. **One operator binary**  
   The process starts a **controller-runtime Manager** (`cmd/main.go`). It hosts **metrics** (TLS, with either platform mTLS or bearer-token protection), **health/readiness probes**, **leader election** (so only one active leader reconciles these controllers), and a **validating admission webhook** for **`NodeHealthCheck`**.

2. **Cluster capabilities**  
   At startup the operator detects **OpenShift**, presence of the **Machine API**, and whether the **control plane topology** is **allowed** for this operator. Those flags affect **webhook admission**, **whether the MachineHealthCheck reconciler is registered**, and **some safety checks** (for example etcd-related behaviour on OpenShift).

3. **MachineHealthCheck coexistence (OpenShift / Machine API only)**  
   A **checker** periodically classifies **MachineHealthCheck** objects in the cluster: none, **multiple or “custom”** (which **disables NodeHealthCheck** to avoid conflicts), or **exactly one** “**termination handler**” MHC (only the **Terminating** unhealthy condition). In the latter case, NHC **ignores** nodes that carry the **Terminating** node condition. When this classification **changes**, NHC reconciles are **signalled** so **`NodeHealthCheck`** objects re-evaluate.

4. **`NodeHealthCheck` reconciler (main loop)**  
   For each **`NodeHealthCheck`** CR, the reconciler roughly does the following, in order:
   - **Lease:** Acquire or use a **per–NodeHealthCheck** lease identity so concurrent reconciles for the same NHC name do not fight each other.
   - **Healthy delay context:** If **`spec.healthyDelay`** is set, downstream logic uses it to **delay** treating a node as healthy again after unhealthy conditions clear (including interaction with remediation CR annotations such as **`remediation.medik8s.io/healthy-delay`**). A **negative** **`healthyDelay`** means NHC will **never** consider the node healthy again automatically—**manual intervention** is expected (per API semantics).
   - **Disable if MHC conflicts:** If the checker says NHC must be off, set **`Disabled`** and stop.
   - **Validate templates:** Ensure **every** referenced remediation template exists and has a usable **`spec.template`**. If not, set **`Disabled`** (template not found / invalid) and **requeue** on not-found with a short delay so creation-order races can heal.
   - **Clear `Disabled` when healthy:** If templates are valid and there is no MHC conflict, clear **`Disabled`** and record **enabled** status.
   - **Dynamic watches:** Register watches for the **template kinds** and **remediation kinds** this NHC uses, so template/remediation changes enqueue the right NHC.
   - **Select nodes:** List nodes matching **`spec.selector`**.
   - **Classify node health:** For each node, apply **`unhealthyConditions`** with **OR** semantics and **duration** measured from the condition’s **`lastTransitionTime`**. Nodes can be **healthy**, **soon-to-be unhealthy** (requeue when duration elapses), or **matching unhealthy**. Nodes handled by the **termination MHC** path are treated as **healthy** from NHC’s perspective.
   - **Cluster upgrade gate:** If the **upgrade checker** reports an ongoing upgrade (OpenShift-oriented signals), **postpone new remediations** and requeue on a short timer. If the checker errors, the code **continues** as if not upgrading.
   - **Pause gate:** If **`spec.pauseRequests`** is non-empty, **do not start new remediations** (in-flight remediations are not cancelled here).
   - **Orphan remediation cleanup:** Remove remediation CRs that **no longer have a node** in the selected set, with special handling when **permanent node deletion** is expected until remediation reports **Succeeded**.
   - **Healthy nodes:** For nodes that no longer match unhealthy rules, **delete** or **finish** remediation CRs as appropriate, update **`unhealthyNodes`** / metrics, and track timestamps when conditions became healthy but CRs are still deleting.
   - **Counts:** Update **observed** and **healthy** node counts in status.
   - **If there are matching unhealthy nodes:**
     - **minHealthy / maxUnhealthy:** If the **minimum healthy** requirement is **not** met, **skip starting new remediations** for this pass (with events).
     - **Storm recovery:** If **`spec.stormCooldownDuration`** is set, evaluate **storm** state. When a “storm” is active or in **cooldown**, **skip new remediations** even if individual nodes look bad—this gives node status time to converge after a widespread incident. Status conditions **`StormActive`** and **`StormCooldownActive`** record that state.
     - **Per unhealthy node:** Skip if the node has the **exclude-from-remediation** label. Otherwise **remediate**: enforce **control-plane** rules (at most one concurrent **different** control-plane remediation in the general case; on OpenShift also consult **etcd disruption** allowability), pick **current template** (single template vs **escalating** chain), **create** remediation CR if appropriate subject to **leases**, handle **timeouts** and **escalation** (annotate remediation CR with **`remediation.medik8s.io/nhc-timed-out`** when a step times out or fails before success, advance to next escalating step), and watch for **very old** remediation CRs for alerting/metrics.

5. **Escalating remediation (within step 4)**  
   When **`escalatingRemediations`** is configured, templates are considered in **`order`**. The reconciler treats a step as **finished unsuccessfully** when the corresponding entry in **`status.unhealthyNodes[].remediations`** has **`timedOut` set**; until then, that step remains **current**. When all steps have **timed out**, there is **no template left** for that node and escalation stops. **Lease duration** logic accounts for the **current** step timeout and can include the **sum of prior steps’ timeouts** so the lock covers the whole chain.

6. **`MachineHealthCheck` reconciler (conditional)**  
   If the cluster has the **Machine API**, a **second** reconciler runs for **`MachineHealthCheck`** resources. It is **separate** from the **`NodeHealthCheck`** loop but shares some infrastructure (for example upgrade awareness and watch helpers as wired in `cmd/main.go`).

7. **Initializer**  
   A startup runnable performs **install-time** tasks such as **RBAC aggregation**, **console** integration, and **ServiceMonitor** setup on OpenShift. It does **not** substitute for user-defined **`NodeHealthCheck`** CRs.

8. **Status phase**  
   After reconciliation, status **`phase`** is derived from **`Disabled`**, **`pauseRequests`**, and whether any **`unhealthyNodes`** still carry **active remediations** (non-empty remediation list). **`lastUpdateTime`** is updated when status changes; the controller may **wait briefly** for the cached object to catch up after a status patch.

## Important details

- **Webhook (validating):** Enforces **non-empty selector**, **exactly one** of **`minHealthy` / `maxUnhealthy`**, **exactly one** of **`remediationTemplate` / `escalatingRemediations`**, escalating **order** uniqueness, escalating **timeout ≥ 1 minute**, rules about **duplicate template kinds** unless templates support **multiple-template** mode, **unsupported control plane topology** on OpenShift, and **forbidden changes** to selector/templates/escalation (and **forbidden delete** of the NHC) while remediations are **in flight**. **`maxUnhealthy`** must **not** be combined with **`MachineDeletionRemediationTemplate`** (neither as the single **`remediationTemplate`** nor inside **`escalatingRemediations`**)—admission and validation reject that combination.
- **Metrics endpoint:** Binds on **8443** with TLS; authentication is either **mTLS** (when cert material is present) or **Kubernetes bearer-token** authz via controller-runtime filters.
- **Remediation CR labelling:** Control-plane remediations may receive **`remediation.medik8s.io/isControlPlaneNode`** so the reconciler can detect **in-flight** control-plane work across nodes.

## Related pieces

- **`overview.md`** — what NHC is for, escalating vs single template at a high level.
- **`failure_modes.md`** — what you **see** when templates are missing, MHC disables NHC, storm blocks work, leases fail, etc.
- **`runbook.md`** — **`kubectl`** checks and fields to inspect.
- **`code_map.md`** — repository file index (`github.com/medik8s/node-healthcheck-operator`).

## Scope

This document does **not** enumerate **every** event name, metric name, or **RBAC** verb. It does **not** replace **`failure_modes.md`** for “why is my node not getting a CR” debugging trees.
