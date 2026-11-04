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

## Installation without OLM

On Kubernetes without OLM, NHC can be installed with the `install.yaml` manifest attached to each
[release](https://github.com/medik8s/node-healthcheck-operator/releases). It needs
[cert-manager](https://cert-manager.io/docs/installation/) for the webhook serving certificate.

```bash
kubectl apply --server-side -f https://github.com/medik8s/node-healthcheck-operator/releases/download/v<version>/install.yaml
```

NHC is installed into the `node-healthcheck-operator-system` namespace.
The manifest is built from `config/standalone`, so it can also be used as a kustomize base.
The image tag in the repository is a placeholder, so set it to the release you install:

```yaml
resources:
- https://github.com/medik8s/node-healthcheck-operator//config/standalone?ref=v<version>
images:
- name: quay.io/medik8s/node-healthcheck-operator
  newTag: v<version>
```

To build the manifest from source, run `make build-installer VERSION=<version>`, with the version
without the leading `v`, which writes `dist/install.yaml`.
Use `IMG` to set a different operator image.

### For development

For instructions on how to install NHC for development, please visit
the [contribution guide](./contributing.md)

## Configuration

For configuration of NHC, please follow instructions in the [configuration guide](./configuration.md).

## Installing a remediator

You need to install a remediator manually.
You can find a list of remediators known to work with NHC on the
[Medik8s website](https://www.medik8s.io/remediation/remediation/#implementations)