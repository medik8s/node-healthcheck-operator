# User Workload Prometheus configuration

Use the following steps to create a bundle predisposed for OpenShift User Workload Prometheus (UWP) Monitoring [1].

1. Configure the Monitoring stack, if not already done [2]
2. Enable monitoring for user-defined projects [1]
3. Create NHC bundle predisposed for UWP

    ```bash
    make bundle-build-metrics-ocp
    ```

    This will:

    - create the `node-healthcheck-ca-bundle` ConfigMap which will have the CA bundle injected for usage in the cluster
    - modify the `node-healthcheck-controller-manager-metrics-service` to create the `node-healthcheck-tls` Secret
    - modify NHC Deployment to mount the above mentioned Secret as volume to be used for `kube-rbac-proxy` TLS configuration

4. Once the bundle is installed, create a new UWP token Secret from an existing `prometheus-user-workload-token` Secret

    IMPORTANT NOTE: use the appropriate namespace, e.g. openshift-operators

    ```bash
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

5. Create a new ServiceMonitor from `config/optional/prometheus-ocp/monitor.yaml` in the `openshift-operators` namespace

    ```bash
    sed -i 's/system/openshift-operators/g' monitor.yaml
    kubectl apply -f monitor.yaml
    ```

[1]: https://docs.openshift.com/container-platform/4.14/monitoring/enabling-monitoring-for-user-defined-projects.html
[2]: https://docs.openshift.com/container-platform/4.14/monitoring/configuring-the-monitoring-stack.html
