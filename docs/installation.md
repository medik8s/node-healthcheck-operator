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

On Kubernetes without OLM, NHC can be installed with the `install.yaml` manifest attached to the
[releases](https://github.com/medik8s/node-healthcheck-operator/releases). Older releases don't have
this asset; install those with OLM. It needs
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

### Using Helm

The Helm chart installs the same resources and also needs cert-manager.
It is published with the releases (older releases have no chart; install those with OLM).
Use the release version without the leading `v`:

```bash
helm install node-healthcheck-operator oci://quay.io/medik8s/charts/node-healthcheck-operator \
  --version <version> \
  --namespace node-healthcheck-operator-system --create-namespace
```

To install from source, check out the repository at a release tag that includes the chart and use
`dist/chart` instead of the OCI reference. Its default image tag follows the repository's default
version, not the release tag, so also pass `--set manager.image.tag=v<version>`.

See `dist/chart/values.yaml` for the configurable values.
The CRD is kept when the chart is uninstalled, so existing NodeHealthCheck resources survive a reinstall.

The chart is generated from `dist/install.yaml` with the kubebuilder Helm plugin.
After changing `config/`, run `make helm-chart`, update `dist/chart/values.yaml` and commit the
changes in `dist/chart`. `make verify-helm-chart` regenerates the chart and fails when that leaves
uncommitted changes, so run it on a clean `dist/chart`.

### For development

For instructions on how to install NHC for development, please visit
the [contribution guide](./contributing.md)

## Configuration

For configuration of NHC, please follow instructions in the [configuration guide](./configuration.md).

## Installing a remediator

You need to install a remediator manually.
You can find a list of remediators known to work with NHC on the
[Medik8s website](https://www.medik8s.io/remediation/remediation/#implementations)