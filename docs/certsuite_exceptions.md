# Certsuite Best Practices Exceptions for Node HealthCheck Operator

This document justifies Node HealthCheck operator (NHC) configurations that deviate from Red Hat's [certsuite](https://github.com/redhat-best-practices-for-k8s/certsuite) (Cloud-native best practices test suite for Kubernetes workloads). The NHC operator follows these best practices where applicable, but requires specific exceptions that are architectural requirements for cluster-wide node health monitoring.

---

## 1. [`lifecycle-pod-toleration-bypass`](https://github.com/redhat-best-practices-for-k8s/certsuite/blob/main/CATALOG.md#lifecycle-pod-toleration-bypass)

The NHC operator uses non-default tolerations for master, control-plane, and infrastructure node taints because it must monitor **all nodes** in the cluster, not just worker nodes.

### Justification

Without these tolerations, control-plane and infrastructure nodes would lack health monitoring. In failure scenarios where all worker nodes are unhealthy, the operator must run on control-plane or infra nodes to perform its core function.

The `NoExecute` toleration for infrastructure nodes prevents pod eviction during node pressure events, ensuring continuous monitoring even when the node is under resource constraints.

The operator uses `priorityClassName: system-cluster-critical`, confirming its infrastructure-level role. All Medik8s node-monitoring operators (Node Maintenance Operator, Self Node Remediation) use identical tolerations.

**Required tolerations:**
```yaml
tolerations:
  - key: "node-role.kubernetes.io/master"
    operator: "Exists"
    effect: "NoSchedule"
  - key: "node-role.kubernetes.io/control-plane"
    operator: "Exists"
    effect: "NoSchedule"
  - key: "node-role.kubernetes.io/infra"
    operator: "Exists"
    effect: "NoSchedule"
  - key: "node-role.kubernetes.io/infra"
    operator: "Exists"
    effect: "NoExecute"
```

---

## 2. [`access-control-pod-role-bindings`](https://github.com/redhat-best-practices-for-k8s/certsuite/blob/main/CATALOG.md#access-control-pod-role-bindings)

The RoleBinding `manager-rolebinding` in the `kube-system` namespace grants the operator read access to the `extension-apiserver-authentication` ConfigMap for metrics mTLS authentication.

### Justification

The NHC operator serves metrics to Platform Prometheus via mTLS. The metrics server reads the cluster's client CA bundle from the `extension-apiserver-authentication` ConfigMap to verify Prometheus client certificates.

This RoleBinding is defined in `config/optional/kube-system-rbac/` and follows the standard pattern for OpenShift operators serving authenticated metrics endpoints.

**RoleBinding details:**
- **Name:** `manager-rolebinding`
- **Namespace:** `kube-system`
- **Role:** `manager-role` (grants get/list/watch on `extension-apiserver-authentication` ConfigMap)
- **Purpose:** Read client CA bundle for metrics mTLS authentication
- **Code reference:** `internal/metrics/tls/configure.go` (ConfigureMTLS function)

---

## Summary

Both configurations are architectural requirements:
- **Toleration-bypass:** Enables monitoring of all cluster nodes (control-plane, infra, worker). Includes `NoExecute` toleration for infra nodes to prevent eviction during node pressure.
- **Pod-role-bindings:** Required for metrics mTLS authentication with Platform Prometheus
