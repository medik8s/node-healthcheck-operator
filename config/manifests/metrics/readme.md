# Complete the User Workload Prometheus configuration with the following steps

1. Create a new Prometheus User Workload token secret under the correct namespace from an existing prometheus-user-workload-token Secret

NOTE: use the appropriate namespace, e.g. openshift-operators

```
# Get the existing prometheus-user-workload-token Secret
existingPrometheusTokenSecret=$(kubectl get secret --namespace openshift-user-workload-monitoring | grep prometheus-user-workload-token | awk '{print $1}')

# Create a new Secret in the openshift-operators namespace
kubectl get secret ${existingPrometheusTokenSecret} --namespace=openshift-user-workload-monitoring -o yaml | \
    sed '/namespace: .*==/d;/ca.crt:/d;/serviceCa.crt/d;/creationTimestamp:/d;/resourceVersion:/d;/uid:/d;/annotations/d;/kubernetes.io/d;' | \
    sed 's/namespace: .*/namespace: openshift-operators/' | \
    sed 's/name: .*/name: prometheus-user-workload-token/' | \
    sed 's/type: .*/type: Opaque/' | \
    > prom-token.yaml

kubectl apply -f prom-token.yaml
```

2. Create a new ServiceMonitor from config/optional/prometheus/monitor.yaml, with the appropriate namespace.

```bash
# Replace the namespace placeholder (system) with the appropriate namespace (e.g. openshift-operators)
sed -i 's/system/openshift-operators/g' monitor.yaml
kubectl apply -f monitor.yaml
```

