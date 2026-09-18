#!/bin/bash
# local-run.sh — Replicate the Kind e2e GitHub Actions workflow locally
#
# Uses local checkouts of NHC and tools instead of cloning from GitHub.
# SNR is deployed from quay.io (matching the GitHub Actions workflow).
# Assumes standard medik8s directory layout:
#   upstream/operators/node-healthcheck-operator  (this repo)
#   upstream/shared/tools
#
# Usage:
#   ./hack/local-run.sh              # Full run (setup + build + deploy + test)
#   ./hack/local-run.sh --skip-setup # Skip cluster creation (reuse existing)
#   ./hack/local-run.sh --skip-build # Skip build and deploy (reuse existing)
#   ./hack/local-run.sh --teardown   # Tear down the cluster

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NHC_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TOOLS_DIR="${NHC_DIR}/../../shared/tools"

# --- Configuration (mirrors GitHub Actions env) ---
export MEDIK8S_CLUSTER_NAME="${MEDIK8S_CLUSTER_NAME:-medik8s-ci}"
if [ -z "${CONTAINER_TOOL:-}" ]; then
    if command -v podman &>/dev/null; then
        export CONTAINER_TOOL=podman
    elif command -v docker &>/dev/null; then
        export CONTAINER_TOOL=docker
    else
        echo "Error: neither podman nor docker found in PATH"
        exit 1
    fi
fi
export CONTAINER_TOOL
export MEDIK8S_REGISTRY_NAME="${MEDIK8S_REGISTRY_NAME:-kind-registry}"
export MEDIK8S_REGISTRY_PORT="${MEDIK8S_REGISTRY_PORT:-5000}"
export IMAGE_REGISTRY="${IMAGE_REGISTRY:-${MEDIK8S_REGISTRY_NAME}:${MEDIK8S_REGISTRY_PORT}}"
export OPM_RENDER_FLAGS="${OPM_RENDER_FLAGS:---skip-tls-verify}"
export DEPLOY_SNR_NAMESPACE="${DEPLOY_SNR_NAMESPACE:-snr-system}"
export DEPLOY_NHC_NAMESPACE="${DEPLOY_NHC_NAMESPACE:-k8s-test}"
export TOOLS_DIR

NHC_IMG="${IMAGE_REGISTRY}/node-healthcheck-operator:latest"
NHC_BUNDLE="${IMAGE_REGISTRY}/node-healthcheck-operator-bundle:latest"

SKIP_SETUP=false
SKIP_BUILD=false
TEARDOWN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-setup) SKIP_SETUP=true; shift ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --teardown)   TEARDOWN=true; shift ;;
        -h|--help)
            echo "Usage: $0 [--skip-setup] [--skip-build] [--teardown]"
            echo ""
            echo "Replicates the Kind e2e GitHub Actions workflow locally."
            echo ""
            echo "Options:"
            echo "  --skip-setup   Skip cluster creation (reuse existing)"
            echo "  --skip-build   Skip build and deploy (reuse existing images/deployment)"
            echo "  --teardown     Tear down the cluster and exit"
            echo ""
            echo "Environment variables:"
            echo "  MEDIK8S_CLUSTER_NAME    Kind cluster name (default: medik8s-ci)"
            echo "  CONTAINER_TOOL          Container tool (default: auto-detect podman/docker)"
            echo "  DEPLOY_SNR_NAMESPACE    Namespace for SNR (default: snr-system)"
            echo "  DEPLOY_NHC_NAMESPACE    Namespace for NHC (default: k8s-test)"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# --- Validate local directories ---
if [ ! -d "${TOOLS_DIR}" ]; then
    echo "Error: Tools directory not found at ${TOOLS_DIR}"
    echo "Expected standard layout: upstream/shared/tools"
    exit 1
fi

echo "=== Local repositories ==="
echo "  NHC:   ${NHC_DIR}"
echo "         branch: $(cd "${NHC_DIR}" && git branch --show-current)"
echo "         commit: $(cd "${NHC_DIR}" && git log --oneline -1)"
echo "  Tools: ${TOOLS_DIR}"
echo "         branch: $(cd "${TOOLS_DIR}" && git branch --show-current)"
echo "         commit: $(cd "${TOOLS_DIR}" && git log --oneline -1)"
echo ""

step() {
    echo ""
    echo "========================================"
    echo "  $1"
    echo "========================================"
}

# kubectl wait --for=create requires 1.31+; poll instead for compatibility
wait_for_resource() {
    local ns="$1" resource="$2" timeout="$3"
    local elapsed=0
    echo "  Waiting for ${resource} in ${ns} (timeout ${timeout}s)..."
    while ! kubectl -n "${ns}" get "${resource}" >/dev/null 2>&1; do
        if [ "${elapsed}" -ge "${timeout}" ]; then
            echo "  Timed out waiting for ${resource}" >&2
            return 1
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    echo "  Found ${resource}."
}

wait_for_cluster_resource() {
    local resource="$1" timeout="$2"
    local elapsed=0
    echo "  Waiting for ${resource} (timeout ${timeout}s)..."
    while ! kubectl get "${resource}" >/dev/null 2>&1; do
        if [ "${elapsed}" -ge "${timeout}" ]; then
            echo "  Timed out waiting for ${resource}" >&2
            return 1
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    echo "  Found ${resource}."
}

image_exists() {
    "${CONTAINER_TOOL}" image inspect "$1" >/dev/null 2>&1
}

check_and_cleanup_existing_build() {
    local found=false
    local image

    echo "=== Checking for existing local build artifacts ==="
    for image in "${NHC_IMG}" "${NHC_BUNDLE}"; do
        if image_exists "${image}"; then
            echo "  Found image: ${image}"
            found=true
        fi
    done

    if [ "${found}" = true ]; then
        echo "  Removing existing local operator images..."
        for image in "${NHC_IMG}" "${NHC_BUNDLE}"; do
            "${CONTAINER_TOOL}" image rm -f "${image}" >/dev/null 2>&1 || true
        done
        echo "  Existing local build artifacts removed."
    else
        echo "  No existing local operator images found."
    fi
}

deployed_resources() {
    local ns="$1"
    local pattern="$2"
    kubectl get subscriptions,csv,deployments -n "${ns}" \
        -o name 2>/dev/null | grep -E "${pattern}" || true
}

check_and_cleanup_existing_deployment() {
    local resources

    echo "=== Checking for existing operator deployments ==="

    resources="$(deployed_resources "${DEPLOY_SNR_NAMESPACE}" 'self-node-remediation')"
    if [ -n "${resources}" ]; then
        echo "  Found existing SNR resources in ${DEPLOY_SNR_NAMESPACE}:"
        echo "${resources}" | sed 's/^/    /'
        echo "  Removing existing SNR OLM installation..."
        operator-sdk -n "${DEPLOY_SNR_NAMESPACE}" cleanup self-node-remediation || true
    fi

    resources="$(deployed_resources "${DEPLOY_NHC_NAMESPACE}" 'node-healthcheck-operator')"
    if [ -n "${resources}" ]; then
        echo "  Found existing NHC resources in ${DEPLOY_NHC_NAMESPACE}:"
        echo "${resources}" | sed 's/^/    /'
        echo "  Removing existing NHC OLM installation..."
        operator-sdk -n "${DEPLOY_NHC_NAMESPACE}" cleanup node-healthcheck-operator --delete-all || true
    fi

    echo "  Existing deployments cleaned up."
}

# --- Teardown ---
if [ "${TEARDOWN}" = true ]; then
    step "Tearing down cluster"
    cd "${NHC_DIR}"
    make dev-teardown 2>/dev/null || true
    exit 0
fi

# --- Setup ---
if [ "${SKIP_SETUP}" = false ]; then
    step "Installing operator-sdk"
    cd "${NHC_DIR}"
    make operator-sdk
    export PATH="${NHC_DIR}/bin:${PATH}"

    step "Creating Kind cluster with registry, OLM, and cert-manager"
    cd "${NHC_DIR}"
    make dev-setup

    step "Cluster info"
    cd "${NHC_DIR}"
    make dev-cluster-info
else
    echo "Skipping setup (--skip-setup)"
    export PATH="${NHC_DIR}/bin:${PATH}"
fi

# --- Build and deploy ---
if [ "${SKIP_BUILD}" = false ]; then
    check_and_cleanup_existing_build
    check_and_cleanup_existing_deployment

    step "Deploying SNR from quay.io via OLM bundle"
    cd "${NHC_DIR}"
    kubectl create ns "${DEPLOY_SNR_NAMESPACE}" 2>/dev/null || true
    kubectl label --overwrite ns "${DEPLOY_SNR_NAMESPACE}" \
        pod-security.kubernetes.io/enforce=privileged \
        pod-security.kubernetes.io/audit=privileged \
        pod-security.kubernetes.io/warn=privileged
    operator-sdk run bundle -n "${DEPLOY_SNR_NAMESPACE}" \
        --timeout 5m \
        quay.io/medik8s/self-node-remediation-operator-bundle:latest

    step "Starting reboot watcher"
    cd "${NHC_DIR}"
    make dev-reboot-watcher

    step "Building and pushing NHC"
    cd "${NHC_DIR}"
    export NHC_SKIP_TEST=true
    make container-build-k8s

    # NHC Makefile hardcodes podman for builds, so push with podman too
    podman push --tls-verify=false "${NHC_IMG}"
    podman push --tls-verify=false "${NHC_BUNDLE}"

    step "Deploying NHC via OLM bundle"
    cd "${NHC_DIR}"
    kubectl create ns "${DEPLOY_NHC_NAMESPACE}" 2>/dev/null || true
    kubectl label --overwrite ns "${DEPLOY_NHC_NAMESPACE}" \
        pod-security.kubernetes.io/enforce=privileged \
        pod-security.kubernetes.io/audit=privileged \
        pod-security.kubernetes.io/warn=privileged

    operator-sdk run bundle -n "${DEPLOY_NHC_NAMESPACE}" --use-http \
        --timeout 5m \
        "${IMAGE_REGISTRY}/node-healthcheck-operator-bundle:latest"

    # Safety net: explicit RBAC binding in case ClusterRole aggregation is slow.
    kubectl create clusterrolebinding nhc-snr-admin-binding \
        --clusterrole=self-node-remediation-ext-remediation \
        --serviceaccount="${DEPLOY_NHC_NAMESPACE}:node-healthcheck-controller-manager" \
        2>/dev/null || true
else
    echo "Skipping build (--skip-build)"
fi

# --- Wait and verify ---
step "Waiting for operators to be ready"
cd "${NHC_DIR}"

make dev-wait

# Wait for NHC controller to complete leader election and initial reconciliation
wait_for_cluster_resource clusterrole/node-healthcheck-operator-aggregation 120
echo "NHC aggregation ClusterRole found — controller is active."

step "Deployment status"
cd "${NHC_DIR}"
make dev-describe

# --- Run tests ---
step "Running e2e tests"
cd "${NHC_DIR}"
OPERATOR_NS=${DEPLOY_NHC_NAMESPACE} \
SNR_STRATEGY=OutOfServiceTaint \
LABEL_FILTER='!OCP-ONLY' \
make test-e2e || {
    step "Debug (test failed)"
    echo "=== NHC Status ==="
    kubectl get nodehealthchecks -o yaml 2>/dev/null || true
    echo ""
    echo "=== SelfNodeRemediation CRs ==="
    kubectl get selfnoderemediations -A -o yaml 2>/dev/null || true
    echo ""
    echo "=== SelfNodeRemediationTemplates ==="
    kubectl get selfnoderemediationtemplates -A -o yaml 2>/dev/null || true
    echo ""
    echo "=== Node Status ==="
    kubectl get nodes -o wide 2>/dev/null || true
    echo ""
    echo "=== NHC Events ==="
    kubectl get events -A --sort-by=.lastTimestamp --field-selector reason!=Pulling,reason!=Pulled 2>/dev/null \
        | grep -iE 'healthcheck|remediat|disabled|enabled|template|unhealthy' || echo "No NHC-related events"
    echo ""
    make dev-ci-debug
    exit 1
}

echo ""
echo "========================================"
echo "  All tests passed!"
echo "========================================"
