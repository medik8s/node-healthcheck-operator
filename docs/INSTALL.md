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


By default, OLM will resolve the Self Node Remediation operator as a dependency and will install the
latest available version in the current catalog or other catalog with higher
priority.

>NOTE: OLM on k8s install operators under the `operators` namespace while
>      in OCP or OKD it is under `openshift-operators`

Your cluster should have 2 deployments and 1 daemonset:

```shell
$ kubectl get deploy -n operators
NAME                                           READY   UP-TO-DATE   AVAILABLE   AGE
node-healthcheck-operator-controller-manager   1/1     1            1           3m8s
self-node-remediation-controller-manager       1/1     1            1           3m10s

$ kubectl get ds -n operators
NAME                       DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR   AGE
self-node-remediation-ds   3         3         3       3            3           <none>          3h6m
```

### Customizations:
See the [README.md](./README.md) for Node Healthcheck CR customizations.


[operator hub]: https://operatorhub.io/operator/node-healthcheck-operator
[operator-sdk]: https://sdk.operatorframework.io/docs/installation/
