# Build the manager binary
FROM quay.io/centos/centos:stream9 AS builder
RUN yum install git golang -y

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# use latest Go z release
ENV GOTOOLCHAIN=auto

# Ensure correct Go version
RUN export GO_VERSION=$(grep -E "go [[:digit:]]\.[[:digit:]][[:digit:]]" go.mod | awk '{print $2}') && \
    go get go@${GO_VERSION} && \
    go version

# Copy the go source
COPY vendor/ vendor/
COPY version/ version/
COPY main.go main.go
COPY hack/ hack/
COPY api/ api/
COPY metrics/ metrics/
COPY controllers/ controllers/

# for getting version info
COPY .git/ .git/

# Build
RUN ./hack/build.sh

FROM registry.access.redhat.com/ubi9/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
