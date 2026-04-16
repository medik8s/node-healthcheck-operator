#!/bin/bash
#
# verify-platform-metrics.sh
#
# This script verifies that all components required for Platform Prometheus
# metrics collection are correctly configured and working.
#
# Usage: ./hack/verify-platform-metrics.sh [--namespace <ns>]
#
# Exit codes:
#   0 - All checks passed
#   1 - One or more checks failed
#

set -uo pipefail

# =============================================================================
# Main
# =============================================================================

main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --namespace|-n)
                NAMESPACE="$2"
                shift 2
                ;;
            --help|-h)
                echo "Usage: $0 [--namespace <ns>]"
                echo ""
                echo "Verify Platform Prometheus metrics configuration for NHC operator."
                echo ""
                echo "Options:"
                echo "  -n, --namespace  Namespace where NHC is installed (default: openshift-workload-availability)"
                echo "  -h, --help       Show this help message"
                exit 0
                ;;
            *)
                echo "Unknown option: $1"
                exit 1
                ;;
        esac
    done

    echo ""
    echo "╔════════════════════════════════════════════════════════════════╗"
    echo "║  Platform Prometheus Metrics Verification                      ║"
    echo "║  Namespace: $NAMESPACE║"
    echo "╚════════════════════════════════════════════════════════════════╝"

    # Run all checks
    check_namespace_label
    check_servicemonitor
    check_tls_secret
    check_rbac
    check_pods
    check_manager_logs
    check_prometheus_target
    check_metrics_available

    # Print summary and exit with appropriate code
    print_summary
}

# =============================================================================
# Configuration
# =============================================================================

# Default namespace (can be overridden with --namespace flag)
NAMESPACE="${NAMESPACE:-openshift-workload-availability}"

# Colors for output (disabled if not a terminal)
if [[ -t 1 ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m' # No Color
else
    RED='' GREEN='' YELLOW='' BLUE='' NC=''
fi

# Counters for summary
PASSED=0
FAILED=0
WARNINGS=0

# =============================================================================
# Logging Functions
# =============================================================================

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((PASSED++)) || true
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((FAILED++)) || true
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
    ((WARNINGS++)) || true
}

log_section() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

# =============================================================================
# Helper Functions
# =============================================================================

# Check if a resource exists
resource_exists() {
    local resource="$1"
    local name="$2"
    local ns="${3:-$NAMESPACE}"

    oc get "$resource" "$name" -n "$ns" &>/dev/null
}

# Get resource field value
get_field() {
    local resource="$1"
    local name="$2"
    local jsonpath="$3"
    local ns="${4:-$NAMESPACE}"

    oc get "$resource" "$name" -n "$ns" -o jsonpath="$jsonpath" 2>/dev/null
}

# =============================================================================
# Verification Checks
# =============================================================================

check_namespace_label() {
    log_section "Checking Namespace Configuration"

    # Check if namespace exists
    if ! oc get namespace "$NAMESPACE" &>/dev/null; then
        log_fail "Namespace '$NAMESPACE' does not exist"
        return 1
    fi
    log_pass "Namespace '$NAMESPACE' exists"

    # Check for cluster-monitoring label
    local label
    label=$(oc get namespace "$NAMESPACE" -o jsonpath='{.metadata.labels.openshift\.io/cluster-monitoring}' 2>/dev/null || echo "")

    if [[ "$label" == "true" ]]; then
        log_pass "Namespace has 'openshift.io/cluster-monitoring=true' label"
    else
        log_fail "Namespace missing 'openshift.io/cluster-monitoring=true' label"
        log_info "  Fix: oc label namespace $NAMESPACE openshift.io/cluster-monitoring=true"
    fi
}

check_servicemonitor() {
    log_section "Checking ServiceMonitor"

    local sm_name="node-healthcheck-controller-manager-metrics-monitor"

    # Check if ServiceMonitor exists
    if ! resource_exists servicemonitor "$sm_name"; then
        log_fail "ServiceMonitor '$sm_name' not found"
        return 1
    fi
    log_pass "ServiceMonitor '$sm_name' exists"

    # Check scrapeClass configuration
    local scrape_class
    scrape_class=$(get_field servicemonitor "$sm_name" '{.spec.scrapeClass}')

    if [[ "$scrape_class" == "tls-client-certificate-auth" ]]; then
        log_pass "ServiceMonitor uses scrapeClass: tls-client-certificate-auth"
    else
        log_fail "ServiceMonitor scrapeClass is '$scrape_class' (expected: tls-client-certificate-auth)"
    fi
}

check_tls_secret() {
    log_section "Checking TLS Configuration"

    # Check TLS secret (service-ca generated)
    local tls_secret="node-healthcheck-tls"
    if resource_exists secret "$tls_secret"; then
        log_pass "TLS Secret '$tls_secret' exists"
    else
        log_fail "TLS Secret '$tls_secret' not found (should be created by service-ca)"
    fi
}

check_rbac() {
    log_section "Checking RBAC Configuration"

    # Check prometheus-k8s Role (namespace-scoped, with node-healthcheck- prefix from kustomize)
    local role_name="node-healthcheck-prometheus-k8s"
    if resource_exists role "$role_name"; then
        log_pass "Role '$role_name' exists"

        # Verify it has the required rules
        local rules
        rules=$(get_field role "$role_name" '{.rules[*].resources}')
        if echo "$rules" | grep -q "services"; then
            log_pass "Role grants access to services/endpoints/pods"
        else
            log_warn "Role may be missing required resource permissions"
        fi
    else
        log_fail "Role '$role_name' not found"
    fi

    # Check prometheus-k8s RoleBinding (namespace-scoped)
    if resource_exists rolebinding "$role_name"; then
        log_pass "RoleBinding '$role_name' exists"

        # Verify it references the correct ServiceAccount
        local subject_ns
        subject_ns=$(get_field rolebinding "$role_name" '{.subjects[0].namespace}')
        if [[ "$subject_ns" == "openshift-monitoring" ]]; then
            log_pass "RoleBinding references openshift-monitoring namespace"
        else
            log_fail "RoleBinding references wrong namespace: $subject_ns"
        fi

        # Verify it references the correct ServiceAccount name
        local subject_name
        subject_name=$(get_field rolebinding "$role_name" '{.subjects[0].name}')
        if [[ "$subject_name" == "prometheus-k8s" ]]; then
            log_pass "RoleBinding references prometheus-k8s ServiceAccount"
        else
            log_fail "RoleBinding references wrong ServiceAccount: $subject_name"
        fi
    else
        log_fail "RoleBinding '$role_name' not found"
    fi

    # Check that the operator SA can read the client CA from kube-system.
    # This permission may come from different sources depending on the deployment method:
    # - OLM: CSV permissions are promoted to a ClusterRole by OLM
    # - make deploy: explicit Role in kube-system (config/optional/kube-system-rbac/)
    local sa="system:serviceaccount:${NAMESPACE}:node-healthcheck-controller-manager"
    if oc auth can-i get configmaps/extension-apiserver-authentication -n kube-system --as="$sa" &>/dev/null; then
        log_pass "Operator SA can read extension-apiserver-authentication in kube-system"
    else
        log_fail "Operator SA cannot read extension-apiserver-authentication in kube-system (needed for client CA)"
    fi
}

check_pods() {
    log_section "Checking Pod Status"

    local deployment="node-healthcheck-controller-manager"

    # Check if deployment exists
    if ! resource_exists deployment "$deployment"; then
        log_fail "Deployment '$deployment' not found"
        return 1
    fi
    log_pass "Deployment '$deployment' exists"

    # Check replica status
    local ready
    ready=$(get_field deployment "$deployment" '{.status.readyReplicas}')
    local desired
    desired=$(get_field deployment "$deployment" '{.spec.replicas}')

    if [[ "$ready" == "$desired" ]] && [[ "$ready" -gt 0 ]]; then
        log_pass "All replicas ready: $ready/$desired"
    else
        log_fail "Not all replicas ready: ${ready:-0}/${desired:-0}"
    fi

    # Verify manager container has port 8443
    local ports
    ports=$(get_field deployment "$deployment" '{.spec.template.spec.containers[?(@.name=="manager")].ports[*].containerPort}')
    if echo "$ports" | grep -q "8443"; then
        log_pass "Manager container exposes port 8443"
    else
        log_fail "Manager container does not expose port 8443"
    fi

    # Verify manager container has TLS volume mount
    local volume_mounts
    volume_mounts=$(get_field deployment "$deployment" '{.spec.template.spec.containers[?(@.name=="manager")].volumeMounts[*].mountPath}')
    if echo "$volume_mounts" | grep -q "/etc/tls/private"; then
        log_pass "Manager container has TLS volume mount at /etc/tls/private"
    else
        log_fail "Manager container missing TLS volume mount at /etc/tls/private"
    fi

    # Verify only one container (no kube-rbac-proxy sidecar)
    local containers
    containers=$(get_field deployment "$deployment" '{.spec.template.spec.containers[*].name}')
    if echo "$containers" | grep -q "kube-rbac-proxy"; then
        log_warn "kube-rbac-proxy sidecar still present (should have been removed)"
    else
        log_pass "No kube-rbac-proxy sidecar (manager serves mTLS directly)"
    fi
}

check_manager_logs() {
    log_section "Checking Manager Logs"

    # Get logs from manager container
    local logs
    logs=$(oc logs -n "$NAMESPACE" deployment/node-healthcheck-controller-manager -c manager --tail=50 2>&1 || echo "ERROR_GETTING_LOGS")

    if [[ "$logs" == "ERROR_GETTING_LOGS" ]]; then
        log_fail "Could not retrieve manager logs"
        return 1
    fi

    # Check for mTLS setup message
    if echo "$logs" | grep -q "Configuring secure metrics with mTLS"; then
        log_pass "Manager configured secure metrics with mTLS"
    else
        log_warn "mTLS configuration message not found in recent logs"
    fi

    # Check for certificate errors
    if echo "$logs" | grep -qi "unable to create.*cert\|unable to initialize.*cert"; then
        log_fail "Certificate setup errors detected in logs"
        log_info "  Recent error logs:"
        echo "$logs" | grep -i "unable to.*cert" | tail -3 | sed 's/^/    /'
    else
        log_pass "No certificate setup errors in recent logs"
    fi

    # Check for successful startup
    if echo "$logs" | grep -q "starting manager"; then
        log_pass "Manager started successfully"
    else
        log_warn "Manager startup message not found in recent logs"
    fi
}

check_prometheus_target() {
    log_section "Checking Prometheus Target Status"

    log_info "Attempting to query Prometheus API..."

    # Query Prometheus directly via oc exec (works regardless of auth method)
    local targets
    targets=$(oc exec -n openshift-monitoring prometheus-k8s-0 -c prometheus -- \
        curl -s 'http://localhost:9090/api/v1/targets' 2>/dev/null || echo "")

    if [[ -z "$targets" ]] || ! echo "$targets" | jq -e '.status == "success"' &>/dev/null; then
        log_warn "Could not query Prometheus API"
        log_info "  Manual check: oc exec -n openshift-monitoring prometheus-k8s-0 -c prometheus -- curl -s 'http://localhost:9090/api/v1/targets'"
        return 0
    fi

    # Check for NHC target in the operator namespace
    local nhc_targets
    nhc_targets=$(echo "$targets" | jq -c '[.data.activeTargets[] | select(.labels.namespace == "'"$NAMESPACE"'")]' 2>/dev/null || echo "[]")

    local target_count
    target_count=$(echo "$nhc_targets" | jq 'length' 2>/dev/null || echo "0")

    if [[ "$target_count" -eq 0 ]]; then
        log_fail "No Prometheus target found in namespace $NAMESPACE"
        return 1
    fi

    # Get first target's details
    local job_name
    job_name=$(echo "$nhc_targets" | jq -r '.[0].labels.job' 2>/dev/null || echo "unknown")

    local health
    health=$(echo "$nhc_targets" | jq -r '.[0].health' 2>/dev/null || echo "unknown")

    log_info "Found $target_count target(s) in namespace"

    if [[ "$health" == "up" ]]; then
        log_pass "Prometheus target is UP (job: $job_name)"
    else
        log_fail "Prometheus target health: $health (job: $job_name)"
        local last_error
        last_error=$(echo "$nhc_targets" | jq -r '.[0].lastError' 2>/dev/null || echo "")
        [[ -n "$last_error" ]] && log_info "  Last error: $last_error"
    fi
}

check_metrics_available() {
    log_section "Checking Metrics Availability"

    local test_query="up{namespace=\"$NAMESPACE\"}"
    log_info "Testing query: $test_query"

    # Query Prometheus directly via oc exec
    local encoded_query
    encoded_query=$(echo "$test_query" | jq -sRr @uri)

    local result
    result=$(oc exec -n openshift-monitoring prometheus-k8s-0 -c prometheus -- \
        curl -s "http://localhost:9090/api/v1/query?query=$encoded_query" 2>/dev/null || echo "")

    if [[ -z "$result" ]] || ! echo "$result" | jq -e '.status == "success"' &>/dev/null; then
        log_warn "Could not query metrics (Prometheus API not accessible)"
        return 0
    fi

    local status
    status=$(echo "$result" | jq -r '.status' 2>/dev/null || echo "error")

    if [[ "$status" == "success" ]]; then
        local value
        value=$(echo "$result" | jq -r '.data.result[0].value[1]' 2>/dev/null || echo "")
        if [[ "$value" == "1" ]]; then
            log_pass "Metrics query returned up=1"
        else
            log_warn "Metrics query returned: $value"
        fi
    else
        log_fail "Metrics query failed: $status"
    fi
}

print_summary() {
    log_section "Summary"

    echo ""
    echo -e "  ${GREEN}Passed:${NC}   $PASSED"
    echo -e "  ${RED}Failed:${NC}   $FAILED"
    echo -e "  ${YELLOW}Warnings:${NC} $WARNINGS"
    echo ""

    if [[ $FAILED -eq 0 ]]; then
        echo -e "${GREEN}All critical checks passed!${NC}"
        return 0
    else
        echo -e "${RED}Some checks failed. Review the output above for details.${NC}"
        return 1
    fi
}



main "$@"
