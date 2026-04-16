#!/bin/bash
#
# verify-k8s-metrics-authn.sh
#
# Verify that the metrics endpoint on vanilla K8s is protected by
# controller-runtime's FilterProvider (bearer-token authn/authz).
#
# The script deploys the operator via OLM (operator-sdk run bundle), then
# checks:
#   1. No kube-rbac-proxy sidecar is present
#   2. Unauthenticated requests are rejected (HTTP 401/403)
#   3. Requests with a valid token + metrics-reader RBAC get HTTP 200
#   4. Requests with a valid token but no RBAC are rejected (HTTP 403)
#
# Prerequisites:
#   - kind, kubectl, curl, docker (or podman)
#   - operator-sdk is downloaded automatically via `make operator-sdk`
#
# Quick start (full end-to-end):
#
#   ./hack/verify-k8s-metrics-authn.sh --create-cluster
#
#   Creates a kind cluster with a local registry, installs OLM, builds and
#   pushes the operator images, deploys via OLM, and runs the checks.
#   The kind cluster is torn down on exit unless --skip-undeploy is passed.
#
# If you already have a running cluster with the operator deployed:
#
#   ./hack/verify-k8s-metrics-authn.sh --skip-deploy
#
# Test a published bundle (skip local build):
#
#   ./hack/verify-k8s-metrics-authn.sh --create-cluster \
#       --bundle-image quay.io/medik8s/node-healthcheck-operator-bundle:v0.9.0
#
# CI usage (cluster already created by workflow):
#
#   ./hack/verify-k8s-metrics-authn.sh --ci
#
# Exit codes:
#   0 - All checks passed
#   1 - One or more checks failed
#

set -uo pipefail

# =============================================================================
# Configuration
# =============================================================================

IMAGE_REGISTRY="${IMAGE_REGISTRY:-kind-registry:5000}"
OLM_VERSION="${OLM_VERSION:-v0.38.0}"
NAMESPACE="${NAMESPACE:-node-healthcheck-operator-system}"
DEPLOY_NAME="node-healthcheck-controller-manager"
SERVICE_NAME="node-healthcheck-controller-manager-metrics-service"
CLUSTERROLE_NAME="node-healthcheck-metrics-reader"
SA_AUTHORIZED="nhc-metrics-test"
SA_UNAUTHORIZED="nhc-metrics-test-norbac"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
OPERATOR_SDK="${PROJECT_DIR}/bin/operator-sdk"
KIND_CLUSTER_NAME="nhc-metrics-test"
REGISTRY_NAME="kind-registry"
REGISTRY_PORT=5000
CREATE_CLUSTER=false
SKIP_DEPLOY=false
SKIP_UNDEPLOY=false
CI_MODE=false
BUNDLE_IMAGE=""  # when set, skip build and deploy this bundle directly
LOCAL_PORT=0  # will be assigned dynamically
PF_PID=""

# Colors (disabled if not a terminal)
if [[ -t 1 ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED='' GREEN='' YELLOW='' BLUE='' NC=''
fi

PASSED=0
FAILED=0

# =============================================================================
# Helpers
# =============================================================================

log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_pass()    { echo -e "${GREEN}[PASS]${NC} $1"; ((PASSED++)) || true; }
log_fail()    { echo -e "${RED}[FAIL]${NC} $1"; ((FAILED++)) || true; }
log_section() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

cleanup() {
    # Kill port-forward if running
    if [[ -n "$PF_PID" ]] && kill -0 "$PF_PID" 2>/dev/null; then
        kill "$PF_PID" 2>/dev/null || true
        wait "$PF_PID" 2>/dev/null || true
    fi

    # Remove test ServiceAccounts and bindings
    kubectl delete clusterrolebinding "${SA_AUTHORIZED}-binding" --ignore-not-found=true &>/dev/null
    kubectl delete serviceaccount "$SA_AUTHORIZED" --namespace default --ignore-not-found=true &>/dev/null
    kubectl delete serviceaccount "$SA_UNAUTHORIZED" --namespace default --ignore-not-found=true &>/dev/null

    if [[ "$SKIP_UNDEPLOY" == true ]]; then
        log_info "Skipping cleanup (--skip-undeploy)"
    elif [[ "$CREATE_CLUSTER" == true ]]; then
        log_info "Deleting kind cluster ${KIND_CLUSTER_NAME}..."
        kind delete cluster --name "$KIND_CLUSTER_NAME" 2>/dev/null || true
        # Stop the local registry container
        docker rm -f "$REGISTRY_NAME" 2>/dev/null || true
    elif [[ "$SKIP_DEPLOY" == false ]]; then
        log_info "Cleaning up OLM deployment..."
        "$OPERATOR_SDK" cleanup node-healthcheck-operator -n "$NAMESPACE" 2>/dev/null || true
    fi
}

# Wait for a deployment to be available
wait_for_deploy() {
    local ns="$1" name="$2" timeout="$3"
    log_info "Waiting for deployment $name to be ready..."
    if ! kubectl rollout status deployment/"$name" --namespace "$ns" --timeout="${timeout}s" 2>/dev/null; then
        log_fail "Deployment $name did not become ready within ${timeout}s"
        kubectl get pods --namespace "$ns" --selector app.kubernetes.io/component=controller-manager 2>/dev/null
        return 1
    fi
}

# Start port-forward and set LOCAL_PORT
start_port_forward() {
    # Pick a random high port to avoid collisions
    LOCAL_PORT=$(shuf --input-range=10000-60000 --head-count=1)
    kubectl port-forward --namespace "$NAMESPACE" "svc/$SERVICE_NAME" "${LOCAL_PORT}:8443" &>/dev/null &
    PF_PID=$!
    # Give it a moment to establish
    sleep 2
    if ! kill -0 "$PF_PID" 2>/dev/null; then
        log_fail "Port-forward failed to start"
        return 1
    fi
    log_info "Port-forward active on localhost:${LOCAL_PORT} (PID $PF_PID)"
}

# curl the metrics endpoint; returns HTTP status code.
# --insecure is required because the metrics server uses a self-signed TLS
# certificate (auto-generated by controller-runtime). This only skips server
# cert validation; authentication is still enforced via the Bearer token header.
curl_metrics() {
    local extra_args=("$@")
    curl --silent --insecure --output /dev/null --write-out '%{http_code}' \
        "${extra_args[@]}" \
        "https://localhost:${LOCAL_PORT}/metrics" 2>/dev/null
}

# Create a kind cluster with a local Docker registry.
# Based on https://kind.sigs.k8s.io/docs/user/local-registry/
create_kind_cluster_with_registry() {
    log_info "Starting local Docker registry ${REGISTRY_NAME}:${REGISTRY_PORT}..."
    if docker inspect "$REGISTRY_NAME" &>/dev/null; then
        docker rm -f "$REGISTRY_NAME" 2>/dev/null || true
    fi
    docker run -d --restart=always -p "127.0.0.1:${REGISTRY_PORT}:5000" \
        --network bridge --name "$REGISTRY_NAME" registry:2

    log_info "Creating kind cluster ${KIND_CLUSTER_NAME}..."
    if ! kind create cluster --name "$KIND_CLUSTER_NAME" --config "${SCRIPT_DIR}/kind-config.yaml"; then
        log_fail "Failed to create kind cluster"
        exit 1
    fi

    # Connect the registry to the kind network if not already connected
    if ! docker network inspect kind | grep -q "$REGISTRY_NAME"; then
        docker network connect kind "$REGISTRY_NAME" 2>/dev/null || true
    fi

    # Configure containerd to use the local registry
    # https://kind.sigs.k8s.io/docs/user/local-registry/
    local registry_dir="/etc/containerd/certs.d/${REGISTRY_NAME}:${REGISTRY_PORT}"
    for node in $(kind get nodes --name "$KIND_CLUSTER_NAME"); do
        docker exec "$node" mkdir -p "$registry_dir"
        docker exec "$node" bash -c "cat <<EOF > ${registry_dir}/hosts.toml
[host.\"http://${REGISTRY_NAME}:${REGISTRY_PORT}\"]
EOF"
    done

    # Document the local registry
    # https://github.com/kubernetes/enhancements/tree/master/keps/sig-cluster-lifecycle/generic/1755-communicating-a-local-registry
    kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${REGISTRY_PORT}"
    hostFromContainerRuntime: "${REGISTRY_NAME}:${REGISTRY_PORT}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

    log_info "Kind cluster and local registry are ready"
}

# Install OLM into the cluster (skipped if already present)
install_olm() {
    if kubectl get crd clusterserviceversions.operators.coreos.com &>/dev/null; then
        log_info "OLM is already installed, skipping"
        return
    fi
    log_info "Installing OLM ${OLM_VERSION}..."
    if ! curl -sL "https://github.com/operator-framework/operator-lifecycle-manager/releases/download/${OLM_VERSION}/install.sh" | bash -s "$OLM_VERSION"; then
        log_fail "Failed to install OLM"
        exit 1
    fi
    log_info "OLM installed"
}

# =============================================================================
# Checks
# =============================================================================

check_no_sidecar() {
    log_section "1. Verify no kube-rbac-proxy sidecar"

    local containers
    containers=$(kubectl get deployment "$DEPLOY_NAME" --namespace "$NAMESPACE" \
        --output jsonpath='{.spec.template.spec.containers[*].name}' 2>/dev/null)

    if echo "$containers" | grep --quiet "kube-rbac-proxy"; then
        log_fail "kube-rbac-proxy sidecar is still present: $containers"
        return
    fi
    log_pass "Only manager container present: $containers"
}

check_unauthenticated() {
    log_section "2. Unauthenticated request is rejected"

    local code
    code=$(curl_metrics)

    if [[ "$code" == "401" || "$code" == "403" ]]; then
        log_pass "Unauthenticated request returned HTTP $code"
    else
        log_fail "Expected HTTP 401 or 403, got $code"
    fi
}

check_unauthorized_token() {
    log_section "3. Valid token without metrics RBAC is rejected"

    # Create SA with no special bindings
    kubectl create serviceaccount "$SA_UNAUTHORIZED" --namespace default &>/dev/null || true
    local token
    token=$(kubectl create token "$SA_UNAUTHORIZED" --namespace default 2>/dev/null)

    if [[ -z "$token" ]]; then
        log_fail "Could not create token for $SA_UNAUTHORIZED"
        return
    fi

    local code
    code=$(curl_metrics --header "Authorization: Bearer $token")

    if [[ "$code" == "403" ]]; then
        log_pass "Token without RBAC returned HTTP $code"
    else
        log_fail "Expected HTTP 403, got $code"
    fi
}

check_authorized_token() {
    log_section "4. Valid token with metrics-reader RBAC gets metrics"

    # Create SA + bind to the metrics-reader ClusterRole
    kubectl create serviceaccount "$SA_AUTHORIZED" --namespace default &>/dev/null || true
    kubectl create clusterrolebinding "${SA_AUTHORIZED}-binding" \
        --clusterrole="$CLUSTERROLE_NAME" \
        --serviceaccount="default:${SA_AUTHORIZED}" &>/dev/null || true

    local token
    token=$(kubectl create token "$SA_AUTHORIZED" --namespace default 2>/dev/null)

    if [[ -z "$token" ]]; then
        log_fail "Could not create token for $SA_AUTHORIZED"
        return
    fi

    local code
    code=$(curl_metrics --header "Authorization: Bearer $token")

    if [[ "$code" == "200" ]]; then
        log_pass "Authorized request returned HTTP $code"
    else
        log_fail "Expected HTTP 200, got $code"
        # Print body for debugging
        curl --silent --insecure --header "Authorization: Bearer $token" \
            "https://localhost:${LOCAL_PORT}/metrics" 2>/dev/null | head -5
    fi
}

# =============================================================================
# Main
# =============================================================================

main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --create-cluster)
                CREATE_CLUSTER=true; shift ;;
            --ci)
                CI_MODE=true; shift ;;
            --skip-deploy)
                SKIP_DEPLOY=true; shift ;;
            --skip-undeploy)
                SKIP_UNDEPLOY=true; shift ;;
            --bundle-image|-b)
                BUNDLE_IMAGE="$2"; shift 2 ;;
            --help|-h)
                echo "Usage: $0 [--create-cluster] [--ci] [--bundle-image <img>] [--skip-deploy] [--skip-undeploy]"
                echo ""
                echo "Test metrics authn/authz on a vanilla K8s cluster using OLM deployment."
                echo ""
                echo "Options:"
                echo "  --create-cluster    Create a kind cluster with local registry, install OLM,"
                echo "                      and tear down on exit"
                echo "  --ci                CI mode: reuse existing cluster silently (for GitHub Actions)"
                echo "  -b, --bundle-image  Deploy this bundle image instead of building locally"
                echo "                      (e.g. quay.io/medik8s/node-healthcheck-operator-bundle:v0.9.0)"
                echo "  --skip-deploy       Assume operator is already deployed"
                echo "  --skip-undeploy     Leave operator/cluster running after test"
                echo "  -h, --help          Show this help message"
                echo ""
                echo "Environment variables:"
                echo "  IMAGE_REGISTRY      Registry for images (default: kind-registry:5000)"
                echo "  OLM_VERSION         OLM version to install (default: v0.38.0)"
                echo "  NAMESPACE           Operator namespace (default: node-healthcheck-operator-system)"
                echo ""
                echo "Examples:"
                echo "  # Full end-to-end (creates cluster, deploys, tests, tears down):"
                echo "  $0 --create-cluster"
                echo ""
                echo "  # Keep cluster after test for debugging:"
                echo "  $0 --create-cluster --skip-undeploy"
                echo ""
                echo "  # Test a published bundle on an existing cluster:"
                echo "  $0 --bundle-image quay.io/medik8s/node-healthcheck-operator-bundle:v0.9.0"
                echo ""
                echo "  # Test against already-deployed operator:"
                echo "  $0 --skip-deploy"
                echo ""
                echo "  # CI workflow (cluster pre-created by helm/kind-action):"
                echo "  $0 --ci"
                exit 0
                ;;
            *)
                echo "Unknown option: $1"; exit 1 ;;
        esac
    done

    if [[ "$CREATE_CLUSTER" == true && "$CI_MODE" == true ]]; then
        echo "Error: --create-cluster and --ci are mutually exclusive." >&2
        exit 1
    fi

    trap cleanup EXIT

    echo ""
    echo "╔════════════════════════════════════════════════════════════════╗"
    echo "║  K8s Metrics Authentication Test (OLM)                       ║"
    echo "╚════════════════════════════════════════════════════════════════╝"
    echo ""
    log_info "Registry: $IMAGE_REGISTRY"
    log_info "Namespace: $NAMESPACE"

    # --- Cluster setup ---
    if [[ "$CREATE_CLUSTER" == true ]]; then
        log_section "0a. Creating kind cluster with local registry"
        if kind get clusters 2>/dev/null | grep --quiet "^${KIND_CLUSTER_NAME}$"; then
            echo -e "${YELLOW}[WARN]${NC} Kind cluster '${KIND_CLUSTER_NAME}' already exists."
            read -rp "Delete it and create a new one? [y/N] " answer
            if [[ "$answer" =~ ^[Yy]$ ]]; then
                kind delete cluster --name "$KIND_CLUSTER_NAME"
                docker rm -f "$REGISTRY_NAME" 2>/dev/null || true
            else
                log_info "Aborting."
                exit 0
            fi
        fi

        create_kind_cluster_with_registry
    elif [[ "$CI_MODE" == true ]]; then
        # CI mode without --create-cluster: cluster already exists from workflow
        log_info "CI mode: using existing cluster"
    fi

    # --- Deploy ---
    if [[ "$SKIP_DEPLOY" == false ]]; then
        log_section "0b. Deploying operator via OLM"

        # Ensure OLM is installed (idempotent — install.sh is a no-op if
        # OLM resources already exist)
        install_olm

        log_info "Ensuring operator-sdk is available..."
        make -C "$PROJECT_DIR" operator-sdk

        local bundle_img run_bundle_args=()
        if [[ -n "$BUNDLE_IMAGE" ]]; then
            # Use a pre-built bundle from an external registry
            bundle_img="$BUNDLE_IMAGE"
            log_info "Using provided bundle image: $bundle_img"
        else
            # Build and push to the local insecure registry
            bundle_img="${IMAGE_REGISTRY}/node-healthcheck-operator-bundle:latest"
            run_bundle_args+=(--use-http)

            log_info "Building operator images..."
            if ! make -C "$PROJECT_DIR" NHC_SKIP_TEST=true container-build-k8s container-push 2>&1 | tail -20; then
                log_fail "container-build-k8s / container-push failed"
                exit 1
            fi
        fi

        log_info "Creating namespace $NAMESPACE..."
        kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

        log_info "Deploying operator bundle via OLM..."
        if ! "$OPERATOR_SDK" run bundle -n "$NAMESPACE" "${run_bundle_args[@]}" \
            "$bundle_img" 2>&1 | tail -20; then
            log_fail "operator-sdk run bundle failed"
            exit 1
        fi

        # Wait for OLM to create the CSV and deployment
        log_info "Waiting for OLM to reconcile..."
        sleep 30

        wait_for_deploy "$NAMESPACE" "$DEPLOY_NAME" 120 || exit 1
    else
        log_info "Skipping deploy (--skip-deploy)"
        wait_for_deploy "$NAMESPACE" "$DEPLOY_NAME" 30 || exit 1
    fi

    # Port-forward
    start_port_forward || exit 1

    # Run checks
    check_no_sidecar
    check_unauthenticated
    check_unauthorized_token
    check_authorized_token

    # Summary
    log_section "Summary"
    echo ""
    echo -e "  ${GREEN}Passed:${NC} $PASSED"
    echo -e "  ${RED}Failed:${NC} $FAILED"
    echo ""

    if [[ $FAILED -eq 0 ]]; then
        echo -e "${GREEN}All checks passed.${NC}"
        exit 0
    else
        echo -e "${RED}Some checks failed.${NC}"
        exit 1
    fi
}

main "$@"
