# Node Health Check (NHC) — Code map

## Purpose

This document maps **concepts** to **paths** in the upstream operator repo **`github.com/medik8s/node-healthcheck-operator`**. Use it when you want to confirm behaviour in code without spelunking from scratch.

## How to use it

- **Entry and wiring** — `cmd/main.go`  
  Starts the **Manager** (metrics TLS, webhooks, leader election), builds **cluster capabilities**, **upgrade checker**, **MHC checker** (channel into NHC), registers **`NodeHealthCheck`** reconciler, conditionally **`MachineHealthCheck`** reconciler, **validating webhook**, **initializer**, metrics setup.

- **API types** — `api/v1alpha1/`  
  - `nodehealthcheck_types.go` — **`NodeHealthCheck`** spec/status, phases, **Disabled / Storm*** conditions, **UnhealthyNode**, **Remediation**, **EscalatingRemediation**.  
  - `nodehealthcheck_webhook.go` — admission rules, immutability while remediating.  
  - `groupversion_info.go` — group **`remediation.medik8s.io`**, **`v1alpha1`**.

- **NHC reconcile** — `internal/controller/nodehealthcheck_controller.go`  
  Main loop: lease, template validation, watches, node selection, unhealthy classification, upgrade/pause/storm/minHealthy gates, healthy handling, **remediate** (CP, leases, timeouts, escalation, old-CR alert).

- **Template / escalation selection** — `internal/controller/resources/templates.go`  
  **`GetCurrentTemplateWithTimeout`**, **`ValidateTemplates`**, **`NoTemplateLeftError`**, template fetch and **Metal3** namespace check.

- **CR lifecycle / leases** — `internal/controller/resources/manager.go`, `internal/controller/resources/lease.go`, `internal/controller/resources/watch.go`  
  Create/list/update remediation CRs, **WatchManager** dynamic watches, lease errors.

- **Status helpers** — `internal/controller/resources/status.go`  
  **`unhealthyNodes`**, remediation entries, metrics hooks.

- **MHC coexistence** — `internal/controller/mhc/checker.go`  
  List MHCs, **NeedDisableNHC**, **NeedIgnoreNode** (Terminating).

- **Cluster** — `internal/controller/cluster/capabilities.go`, `upgrade_checker.go`  
  OpenShift / Machine API / topology; upgrade deferral.

- **Utilities** — `internal/controller/utils/`  
  **`utils.go`** — **`GetAllRemediationTemplates`**, **`GetRemediationDuration`**, **`GetNodeNameFromCR`**, mappers, etc.  
  **`mapper.go`** — enqueue NHC from Node / MHC events.  
  **`annotations/`** — e.g. **template-name** annotation for CRs.

- **MachineHealthCheck controller** — `internal/controller/machinehealthcheck_controller.go`  
  OpenShift **MHC** reconciliation when Machine API present.

- **Bootstrapping** — `internal/controller/initializer/init.go`  
  RBAC aggregation, console plugin, ServiceMonitor.

- **Feature gates** — `internal/controller/featuregates/accessor.go`  
  Used from MHC wiring in `cmd/main.go`.

- **Metrics** — `metrics/`, `metrics/tls/`  
  Registration and TLS helpers for metrics.

## Important details

- **Generated / vendor** — prefer reading **`api/`** and **`internal/controller/`**; **`vendor/`** is third-party.  
- **E2E** — `e2e/` for behaviour examples, not product docs.

## Related pieces

- **`architecture.md`**, **`failure_modes.md`**, **`runbook.md`** — behaviour and ops without file paths.

## Scope

Not a line-by-line index of every file; not a substitute for **go doc** or API reference.
