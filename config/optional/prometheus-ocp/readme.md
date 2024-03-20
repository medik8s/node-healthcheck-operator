# User Workload Prometheus configuration

Use the following steps to create a bundle predisposed for OpenShift User Workload Prometheus (UWP) Monitoring [1].

1. Configure the Monitoring stack, if not already done [2]
2. Enable monitoring for user-defined projects [3]
3. Ensure NHC bundle with monitoring supported (>= v0.8.0) is installed
4. Once the bundle is installed, create a new UWP token Secret from an existing `prometheus-user-workload-token` Secret

    IMPORTANT NOTE: use the operator's namespace (e.g. `openshift-workload-availability`).

    ```bash
    # Get the existing prometheus-user-workload-token Secret
    existingPrometheusTokenSecret=$(kubectl get secret --namespace openshift-user-workload-monitoring | grep prometheus-user-workload-token | awk '{print $1}')

    # Create a new Secret in the openshift-operators namespace
    kubectl get secret ${existingPrometheusTokenSecret} --namespace=openshift-user-workload-monitoring -o yaml | \
        sed '/namespace: .*==/d;/ca.crt:/d;/serviceCa.crt/d;/creationTimestamp:/d;/resourceVersion:/d;/uid:/d;/annotations/d;/kubernetes.io/d;' | \
        sed 's/namespace: .*/namespace: openshift-workload-availability/' | \
        sed 's/name: .*/name: prometheus-user-workload-token/' | \
        sed 's/type: .*/type: Opaque/' | \
        > prom-token.yaml

    kubectl apply -f prom-token.yaml
    ```

5. Create a new ServiceMonitor from `config/optional/prometheus-ocp/monitor.yaml` in the operator's namespace (e.g. `openshift-workload-availability`)

    ```bash
    sed -i 's/system/openshift-workload-availability/g' monitor.yaml
    kubectl apply -f monitor.yaml
    ```

[1]: https://docs.openshift.com/container-platform/4.14/monitoring/enabling-monitoring-for-user-defined-projects.html
[2]: https://docs.openshift.com/container-platform/4.14/monitoring/configuring-the-monitoring-stack.html
[3]: https://docs.openshift.com/container-platform/4.14/monitoring/enabling-monitoring-for-user-defined-projects.html#enabling-monitoring-for-user-defined-projects_enabling-monitoring-for-user-defined-projects
