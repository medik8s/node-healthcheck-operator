## Prerequisite
If you are using a kubernetes distribution with no running OLM (OCP and OKD has
it by default) you can quickly install it by using the operator-sdk tool.
See the [operator-sdk installation docs][operator-sdk] for help

```shell
$ operator-sdk olm install
```

## Installation

  - For K8S:

    ```shell
     kubectl create -f https://operatorhub.io/install/node-healthcheck-operator.yaml
    ```
  - For OpenShift:

    ```shell
    curl -s -L https://operatorhub.io/install/node-healthcheck-operator.yaml | \
    sed -e '/namespace:/ s/operators/openshift-operators/ ; /source:/ s/operatorhubio-catalog/community-operators/ ; /sourceNamespace:/ s/olm/openshift- marketplace/' |   kubectl create -f -
    ```


By default, OLM will resolve Poison-Pill as a dependency and will install the
latest available version in the current catalog or other catalog with higher
priority.

## Post install

>NOTE: OLM on k8s install operators under the `operators` namespace while
>      in OCP or OKD it is under `openshift-operators`

- create `PoisonPillConfig` object ([ongoing work to make it automatic][ppil-auto-config])
```shell
cat << EOF | kubectl create -f -
apiVersion: poison-pill.medik8s.io/v1alpha1
kind: PoisonPillConfig
metadata:
  namespace: operators
  name: poison-pill-config
spec: {}
EOF
```
- create a poison pill remediation template
```shell
cat << EOF | kubectl create -f -
apiVersion: poison-pill.medik8s.io/v1alpha1
kind: PoisonPillRemediationTemplate
metadata:
  namespace: default
  name: ppill-template
spec:
  template:
    spec: {}
EOF
```

- create Node-Healthcheck CR that points to poison-pill remediation
```shell
cat << EOF | kubectl create -f -
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  namespace: default
  name: nodehealthcheck-sample
spec:
  unhealthyConditions:
    - type: Ready
      status: Unknown
      duration: 300s
    - type: Ready
      status: False
      duration: 300s
  remediationTemplate:
    kind: PoisonPillRemediationTemplate
    apiVersion: poison-pill.medik8s.io/v1alpha1
    name: ppill-template
    namespace: default
EOF
```

Your cluster should have 2 deployments and 1 daemonset:

```shell
$ kubectl get deploy -n operators
NAME                                           READY   UP-TO-DATE   AVAILABLE   AGE
node-healthcheck-operator-controller-manager   1/1     1            1           3m8s
poison-pill-controller-manager                 1/1     1            1           3m10s

$ kubectl get ds -n operators
NAME             DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR   AGE
poison-pill-ds   3         3         3       3            3           <none>          3h6m
```

### Customizations:
See the [README.md](./README.md) for Node Healthcheck CR customizations.


[operator hub]: https://operatorhub.io/operator/node-healthcheck-operator
[ppil-auto-config]: https://github.com/medik8s/poison-pill/pull/33
[operator-sdk]: https://sdk.operatorframework.io/docs/installation/
