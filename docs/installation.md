## Prerequisite

The Node Healthcheck operator is developed using the [Operator SDK](https://sdk.operatorframework.io/)
and designed to be installed using the [Operator Lifecycle Manager (OLM)](https://olm.operatorframework.io/).

If you are using a Kubernetes distribution without OLM (OCP and OKD have it by
default), you can quickly install it using the operator-sdk tool, see the
[OLM installation guide](https://olm.operatorframework.io/docs/getting-started/#installing-olm-in-your-cluster).

## Installation using OperatorHub

### On Kubernetes

NHC is available on [OperatorHub.io](https://operatorhub.io/operator/node-healthcheck-operator),
please follow the instructions there.

### On OKD and OpenShift

Please visit the built-in OperatorHub in the UI, search for "Node Health Check",
and follow the instructions.

> **Note**
> 
> OLM on k8s will install operators in the `operators` namespace,
> while OCP or OKD are using `openshift-operators`

### For development

For instructions on how to install NHC for development, please visit
the [contribution guide](./contributing.md)

## Configuration

For configuration of NHC, please follow instructions in the [configuration guide](./configuration.md).

## Installing a remediator

Currently, NHC has the [Self Node Remediation (SNR) operator](https://www.medik8s.io/remediation/self-node-remediation/self-node-remediation/)
configured as dependency. This means that SNR will be automatically installed by OLM.

If you want to use another remediator, you have to install it manually.
You can find a list of remediators known to work with NHC on the
[Medik8s website](https://www.medik8s.io/remediation/remediation/#implementations)