# Node Health Check (NHC) — Runbook

## Purpose

This document is for **operators**: quick **`kubectl`** checks, which **fields** matter, and **sensible next steps** when behaviour looks wrong. Deep logic is in **`architecture.md`**; symptom mapping is in **`failure_modes.md`**.

## How to use it

1. **Find your `NodeHealthCheck` objects**  
   They are **cluster-scoped**. Short name **`nhc`**:

   ```bash
   kubectl get nhc
   kubectl describe nhc <name>
   ```

2. **Read status first**  
   In order of usefulness:
   - **`status.phase`** and **`status.reason`** — high-level state (**Disabled**, **Paused**, **Remediating**, **Enabled**).
   - **`status.conditions`** — especially **`Disabled`**, **`StormActive`**, **`StormCooldownActive`** (messages carry reasons).
   - **`status.observedNodes`** / **`status.healthyNodes`** — sanity check against **`spec.selector`** and **`minHealthy` / `maxUnhealthy`**.
   - **`status.unhealthyNodes`** — per-node entries; each may list **`remediations`** with **`resource`**, **`started`**, and **`timedOut`** for **escalating** flows.

3. **Read spec second**  
   - **`spec.selector`** — must be **non-empty** (webhook). Confirm it matches the nodes you care about.  
   - **`spec.unhealthyConditions`** — **OR** rules; check **type**, **status**, and **duration** against a node’s **`lastTransitionTime`**.  
   - **`spec.minHealthy`** *or* **`spec.maxUnhealthy`** — exactly one; affects whether new remediations start when many nodes are bad. Do **not** use **`maxUnhealthy`** with **`MachineDeletionRemediationTemplate`** (single or escalating)—admission will **reject** the NHC; use **`minHealthy`** or a different remediator template kind.  
   - **`spec.remediationTemplate`** *or* **`spec.escalatingRemediations`** — exactly one mode. For escalation, confirm **`order`** and **`timeout`** (each timeout **≥ 1 minute** at admission).  
   - **`spec.pauseRequests`** — any non-empty list blocks **new** remediations.  
   - **`spec.stormCooldownDuration`** — if set, storm / cooldown conditions explain delayed remediation.  
   - **`spec.healthyDelay`** — delays treating a node as healthy again after **`unhealthyConditions`** no longer match; may surface on remediation CRs as **`remediation.medik8s.io/healthy-delay`**. A **negative** value means the node is **not** auto-considered healthy—**manual** intervention is required. See **`failure_modes.md`** §8 if behaviour is confusing.

4. **Inspect the target nodes**  
   ```bash
   kubectl get node -l '<labels matching your NHC selector>'
   kubectl describe node <name>
   ```  
   Check **conditions**, and whether **`remediation.medik8s.io/exclude-from-remediation`** is set.

5. **Inspect remediation CRs**  
   Use the **kind/namespace/name** from **`status.unhealthyNodes[].remediations[].resource`**, or list by **owner / labels** if your remediator exposes them. On the remediation object, check:
   - **`status.conditions`** — **`Succeeded`**, **`Processing`**, etc. (exact types depend on remediator).  
   - **Annotations** — especially **`remediation.medik8s.io/nhc-timed-out`** (step ended by NHC timeout/failure path) and **`remediation.medik8s.io/node-name`** if present.  
   - **Labels** — **`remediation.medik8s.io/isControlPlaneNode`** on control-plane remediations.

6. **OpenShift-only checks (when relevant)**  
   - **MachineHealthCheck** count and specs — multiple or “custom” MHCs **disable** NHC. On clusters with the **Machine API**, the operator also runs a separate **MachineHealthCheck** reconciler (see **`architecture.md`**).  
   - **ClusterVersion** / upgrade — NHC may **defer** new remediations while upgrading.  
   - **etcd / PDB** — if control-plane remediation seems blocked, correlate with CP remediation CRs and events.

7. **Operator health**  
   - Deployment / pods in the **operator namespace** (from your install; often set via **`DEPLOYMENT_NAMESPACE`** in the operator).  
   - **Metrics** are served on **TLS** (default bind **8443** in code); scraping depends on your platform (e.g. **ServiceMonitor** on OpenShift).

## Important details

- **Immutability:** While **`status.unhealthyNodes`** shows **active remediations**, you generally **cannot** change **selector**, **remediation template**, or **escalating remediations**, or **delete** the NHC — the **validating webhook** blocks it. Fix the **remediation** or wait for **status** to clear first.  
- **Escalation debugging:** Compare **`timedOut`** on each **`remediation`** entry in NHC status with the **`nhc-timed-out`** annotation and timestamps on the CR.  
- **“No remediation CR”:** Work through **`failure_modes.md`** section 2 in parallel (upgrade, pause, minHealthy, storm, lease, CP, exclude label).  
- **Lease skip:** If remediation **create** is skipped because a **lease** is held, **`kubectl describe nhc <name>`** events often mention the node and a lease / skip reason; wait for **requeue** or resolve the competing holder.  
- **Orphan / deleted node:** If the **node** is gone or no longer matches the selector but a **remediation CR** remains, see **`failure_modes.md`** §5 and check remediation **Succeeded** / **Permanent node deletion expected**-style conditions.

## Related pieces

- **`architecture.md`** — reconcile order.  
- **`failure_modes.md`** — symptom → cause.  
- **`code_map.md`** — where to read in **`github.com/medik8s/node-healthcheck-operator`**.

## Scope

Does **not** document **install** manifests, **OLM** subscription names, or **exact** metric names; those vary by release and packaging. Does **not** replace remediator-specific runbooks (SNR, MDR, …).
