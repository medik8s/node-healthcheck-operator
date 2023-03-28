## Contributing

### Introduction

The Node Healthcheck operator is an application using the [Operator Pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
for running inside a container in a Kubernetes cluster.

For easier development and deployment, it is developed using the
[Operator SDK](https://sdk.operatorframework.io/), and deployed using the
[Operator Lifecycle Manager (OLM)](https://olm.operatorframework.io/).

If you want to develop new features, or fix bugs, and are not familiar with
Operator SDK yet, we recommend to try their [Go Tutorial](https://sdk.operatorframework.io/docs/building-operators/golang/)
first.

### Makefile

The [Makefile](../Makefile) provides multiple targets for testing, building and
running NHC. The most common used targets are mentioned below.

### Running unit tests

In order to verify your changes didn't break anything, you can run `make test`.
It runs unit tests, but also various other pre-build tasks, in order to ensure
that none of them is forgotten.

### Building NHC

The build artifacts of NHC are 3 container images:

- the actual operator image
- a bundle image, containing metadata for OLM
- optionally an index image (also known as catalog image), which packages data
from one or more bundle images for usage by OLM.

These images need to be pushed to an image registry, which can be reached from 
your cluster. The easiest way to do this is:

```shell
export IMAGE_REGISTRY="<registry>/<username>" # e.g. "quay.io/my-username"
export VERSION=0.1.0                          # optional, defaults to NHC version 0.0.1 being pushed with `latest` image tag
make container-build container-push
```

### Running NHC

In case you use Kubernetes and not OKD or Openshift, please
[install OLM](https://olm.operatorframework.io/docs/getting-started/#installing-olm-in-your-cluster)
first. After that you can deploy your NHC version with `make bundle-run`

### Creating a PR

When creating a PR, the operator will be automatically build, deployed and
tested on fresh OpenShift clusters, running on test infrastructure provided
by Red Hat.

Please also update unit tests and ideally the e2e tests according to your
changes. In case of new features, docs updates are appreciated as well.

### Help

If you run into any issues at any stage, feel free to reach out to us on our
[Google group](https://groups.google.com/g/medik8s), or in an incomplete PR.
Please make that PR a "draft" PR then, and prefix the title with "WIP", in order
to save some test resources.
