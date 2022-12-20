# Build the manager binary
FROM quay.io/centos/centos:stream8 AS builder
RUN yum install git golang -y

# Ensure correct Go version
ENV GO_VERSION=1.19
RUN go install golang.org/dl/go${GO_VERSION}@latest
RUN ~/go/bin/go${GO_VERSION} download
RUN /bin/cp -f ~/go/bin/go${GO_VERSION} /usr/bin/go
RUN go version

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

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

FROM registry.access.redhat.com/ubi8/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
