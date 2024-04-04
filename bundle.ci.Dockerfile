FROM quay.io/centos/centos:stream9 AS builder

WORKDIR /workspace

# Install some needed tools
RUN dnf install -y golang which

# Copy needed files and dirs
COPY go.mod go.mod
COPY go.sum go.sum

COPY PROJECT PROJECT
COPY Makefile Makefile
COPY config/ config/
COPY bundle/ bundle/
COPY hack/ hack/

COPY main.go main.go
COPY version/ version/
COPY api/ api/
COPY controllers/ controllers/
COPY metrics/ metrics/
COPY vendor/ vendor/

# Generate OCP bundle without overriding the pullspec set by the CI
RUN make bundle-ocp-ci

# Build bundle image for OCP CI
FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=node-healthcheck-operator
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.33.0
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v3

# Labels for testing.
LABEL operators.operatorframework.io.test.mediatype.v1=scorecard+v1
LABEL operators.operatorframework.io.test.config.v1=tests/scorecard/

# Copy files to locations specified by labels.
COPY --from=builder /workspace/bundle/manifests /manifests/
COPY --from=builder /workspace/bundle/metadata /metadata/
COPY --from=builder /workspace/bundle/tests/scorecard /tests/scorecard/
