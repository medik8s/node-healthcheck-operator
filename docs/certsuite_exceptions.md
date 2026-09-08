# Certsuite Best Practices Exceptions for Node HealthCheck Operator

This document justifies Node HealthCheck operator (NHC) configurations that deviate from Red Hat's [certsuite](https://github.com/redhat-best-practices-for-k8s/certsuite) (Cloud-native best practices test suite for Kubernetes workloads). The NHC operator follows these best practices where applicable, but requires specific exceptions that are architectural requirements for cluster-wide node health monitoring.

---

## 1. [`lifecycle-pod-toleration-bypass`](https://github.com/redhat-best-practices-for-k8s/certsuite/blob/main/CATALOG.md#lifecycle-pod-toleration-bypass)

The NHC operator uses non-default tolerations for master, control-plane, and infrastructure node taints because it must monitor **all nodes** in the cluster, not just worker nodes.

### Justification

Without these tolerations, control-plane and infrastructure nodes would lack health monitoring. In failure scenarios where all worker nodes are unhealthy, the operator must run on control-plane or infra nodes to perform its core function.

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
```

---

## 2. [`access-control-pod-role-bindings`](https://github.com/redhat-best-practices-for-k8s/certsuite/blob/main/CATALOG.md#access-control-pod-role-bindings)

The RoleBinding `service-auth-reader` in the `kube-system` namespace is automatically created by [Operator Lifecycle Manager (OLM)](https://olm.operatorframework.io/) when the operator deploys admission webhooks. This binding is not defined in the operator's codebase and cannot be prevented.

### Justification

The NHC operator uses admission webhooks for `NodeHealthCheck` validation and defaulting. These webhooks require reading the `extension-apiserver-authentication` ConfigMap in `kube-system` for TLS authentication. This is the standard OLM pattern for all webhook-based operators ([Kubernetes documentation](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#authenticate-apiservers)).

**RoleBinding details:**
- **Name:** `node-healthcheck-controller-manager-service-auth-reader`
- **Namespace:** `kube-system`
- **Created by:** OLM (not the operator)

---

## Summary

Both configurations are architectural requirements:
- **Toleration-bypass:** Enables monitoring of all cluster nodes (control-plane, infra, worker)
- **Pod-role-bindings:** Required by OLM for webhook TLS authentication; not under operator control
