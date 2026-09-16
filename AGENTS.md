# AGENTS.md — Node Healthcheck Operator

> **IMPORTANT — read this first.** Before making any changes in this repository, you MUST
> read the medik8s **common agent guide**, the **OFFICIAL guidance** for all medik8s
> operators: **https://github.com/medik8s/.github/blob/main/AGENTS.md** . It is
> authoritative project guidance and must not be ignored.

## Medik8s context

NHC is the **orchestrator/detector** in the [medik8s](https://medik8s.io) family. It watches `NodeConditions` and decides when a node is unhealthy, then delegates fencing to an independent remediation provider (SNR, MDR, FAR, SBR, etc.) by creating a remediation CR from a template. NHC does not fence nodes itself.

The template contract: each remediator defines a `*Template` CRD; the admin wires one (or an escalating list) into the `NodeHealthCheck` CR. NHC instantiates the remediation CR; when the node recovers, NHC deletes the CR to signal success.

## What NHC does

- Evaluates each node's `NodeConditions` against admin-defined criteria in `NodeHealthCheck` CRs.
- Supports single remediator or **escalating remediation**: tries remediators in order with timeouts until the node is healthy.
- Guards against over-remediation: `minHealthy` / `maxUnhealthy` thresholds stop new remediation when too many nodes are already unhealthy.
- Automatically pauses remediation during **cluster upgrades** (OpenShift only, via ClusterVersionOperator).
- Supports manual pause of a `NodeHealthCheck` CR.
- Also reconciles `MachineHealthCheck` resources for compatibility with the MHC contract.

## Build & test

```bash
# Unit tests (also runs generate, fmt, vet, imports)
make test

# Build the operator binary
make manager

# Build + push the operator image (set your registry; defaults to quay.io/medik8s)
export IMAGE_REGISTRY="quay.io/<your-username>"
make docker-build docker-push

# Regenerate CRDs + RBAC after API changes
make manifests generate

# Lint / format
make fmt vet
make fix-imports    # sort imports
make test-imports   # verify imports sorted

# e2e tests (requires running cluster, NHC image built+pushed, SNR deployed)
make test-e2e
```

> `make test` runs unit tests **plus** generate/fmt/vet/imports — do not skip it before a PR.

## Local development & testing

Follow the shared workflow in the
[common agent guide](https://github.com/medik8s/.github/blob/main/AGENTS.md) — it documents
the standardized `dev-*` make targets (`dev-setup`, `dev-deploy`, `dev-redeploy`,
`dev-undeploy`, `dev-describe`, `dev-help`, …) provided by `medik8s/tools` (`dev/dev.mk`).

**To develop and test against a real OpenShift / Kubernetes cluster**:
`export SKIP_KIND=true` before the `dev-*` targets; images are pushed to `ttl.sh`.

Github CI workflow runs on a Kind cluster. When both NHC and a remediator (SNR, FAR, MDR,
SBR) are deployed, `dev-deploy` also creates a `NodeHealthCheck` CR linking them; on a real
OpenShift cluster the full remediation flow runs end-to-end, while on Kind
`make dev-simulate-failure` / `make dev-recover` exercise only the detection/decision flow.

## e2e prerequisites

- Cluster with ≥ 2 rebootable workers (Kubernetes or OpenShift).
- `KUBECONFIG` set, or `~/.kube/config` present.
- NHC image built and pushed (`make docker-build docker-push`).
- SNR must be deployed **before** running e2e — the suite does not build or deploy it. Use
  `make deploy-snr` (pulls SNR's `main` branch via `SNR_GIT_REF`, then builds, pushes, and
  deploys SNR) as a manual prerequisite.

## Key design constraints

- NHC only **creates** and **deletes** remediation CRs — it never modifies node state directly.
- `minHealthy` accepts both integer and percentage string (e.g. `"51%"`). Do not confuse the types in API or tests.
- Escalating remediation order is significant: remediators are tried sequentially; a later one only starts after the earlier one's timeout expires.
- Upgrade detection is OpenShift-specific; on plain Kubernetes the upgrade-pause code path is a no-op.
- MHC reconciler exists for compatibility; prefer `NodeHealthCheck` for new deployments.
- CI builds and deploys NHC on fresh Kind clusters for every PR.

## Security

- Operator requires cluster-scoped RBAC to watch all nodes and create/delete arbitrary remediation CRs.
- No privileged pods; the operator runs with standard controller-manager permissions.
