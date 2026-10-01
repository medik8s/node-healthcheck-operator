# AGENTS.md — Node Healthcheck Operator

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

## Repository layout

```
api/v1alpha1/               NodeHealthCheck CRD types + validation
internal/controller/        NHC + MHC reconcilers, sub-packages:
  cluster/                  Cluster-level helpers (upgrade detection, etc.)
  console/                  OpenShift console integration
  featuregates/             Feature gate support
  initializer/              Controller startup/init logic
  mhc/                      MachineHealthCheck reconciler
  rbac/                     RBAC role aggregation for remediators
  resources/                Remediation CR lifecycle (create/delete templates)
  servicemonitor/           Prometheus ServiceMonitor management
  utils/                    Shared controller utilities (conditions, events, mappers)
cmd/                        Operator main package
e2e/                        Ginkgo e2e suite (requires live cluster + SNR)
docs/                       Configuration, workflow, troubleshooting, installation guides
config/                     Kustomize bases (operator, rbac, bundle)
bundle/                     Generated OLM bundle (manifests, metadata, tests)
version/                    Operator version package
hack/                       Dev scripts
```

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

## Local development & deployment

Deploying to a dev cluster is standardized across all medik8s operators via the
shared dev environment in [`medik8s/tools`](https://github.com/medik8s/tools)
(`dev/dev.mk`). The Makefile pulls these targets in automatically: it uses a
sibling `../tools` checkout if present, otherwise shallow-clones the repo into
`.tools/` on first `make dev-*` use.

```bash
make dev-setup       # Create a Kind cluster (1 control-plane + 3 workers) with deps
make dev-deploy      # Build image, load it, install CRDs, deploy the operator
make dev-describe    # Summarize nodes, pods, CRs, leases, and events
make dev-redeploy    # Rebuild and restart pods (fast iteration)
make dev-undeploy    # Remove the operator
make dev-teardown    # Destroy the Kind cluster
make dev-help        # List all dev-* targets
```

Deploy to an existing cluster (OCP, etc.) with `SKIP_KIND=true`; images are
pushed to the ephemeral `ttl.sh` registry:

```bash
export KUBECONFIG=~/.kube/my-cluster
SKIP_KIND=true make dev-setup dev-deploy
```

When both NHC and a remediator (SNR, FAR, or MDR) are deployed, `dev-deploy` also
creates a `NodeHealthCheck` CR linking them. Trigger the flow with `make
dev-simulate-failure` and restore with `make dev-recover` on Kind. See
[`dev/README.md`](https://github.com/medik8s/tools/blob/main/dev/README.md) in
`medik8s/tools` for prerequisites, all targets, and per-operator coverage.

## e2e prerequisites

- Cluster with ≥ 2 rebootable workers (Kubernetes or OpenShift ≥ 4.13).
- `KUBECONFIG` set, or `~/.kube/config` present.
- NHC image built and pushed (`make docker-build docker-push`).
- e2e pulls SNR from its `main` branch and builds/deploys it automatically.

## Code style

- Go, Kubebuilder v4, controller-runtime; follows standard medik8s patterns.
- Imports must be sorted (`make fix-imports`).
- No new direct commits to `main`; open a PR (CI builds + deploys on fresh OCP clusters).
- Mark draft PRs with "WIP" in the title to avoid consuming test resources.

## Key design constraints

- NHC only **creates** and **deletes** remediation CRs — it never modifies node state directly.
- `minHealthy` accepts both integer and percentage string (e.g. `"51%"`). Do not confuse the types in API or tests.
- Escalating remediation order is significant: remediators are tried sequentially; a later one only starts after the earlier one's timeout expires.
- Upgrade detection is OpenShift-specific; on plain Kubernetes the upgrade-pause code path is a no-op.
- MHC reconciler exists for compatibility; prefer `NodeHealthCheck` for new deployments.

## Security

- Operator requires cluster-scoped RBAC to watch all nodes and create/delete arbitrary remediation CRs.
- No privileged pods; the operator runs with standard controller-manager permissions.
- Never widen RBAC beyond the generated `config/rbac/` manifests without review.

## Keeping the docs current

If your changes affect anything described here — build commands, repo layout, CRD semantics, remediation flow, over-remediation logic, e2e setup — or any other existing documentation (`README.md`, `CONTRIBUTING.md`, anything under `docs/`, inline command or usage references), update all of it so the docs never drift from the code.

## Commit conventions

- Reference the relevant issue or PR number when applicable.
