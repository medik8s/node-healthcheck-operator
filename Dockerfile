# Build the manager binary
FROM quay.io/centos/centos:stream9 AS builder
RUN dnf install -y jq git \
    && dnf clean all -y

WORKDIR /workspace

# Copy the Go Modules manifests for detecting Go version
COPY go.mod go.mod
COPY go.sum go.sum

RUN \
    # get Go version from mod file
    export GO_VERSION=$(grep -oE "toolchain go[[:digit:]]\.[[:digit:]]+\.[[:digit:]]" go.mod | awk '{print $2}') && \
    echo ${GO_VERSION} && \
    # Match the Go toolchain to the architecture of the build container.
    case "$(uname -m)" in \
        x86_64) GO_ARCH=amd64 ;; \
        aarch64) GO_ARCH=arm64 ;; \
        ppc64le|s390x) GO_ARCH=$(uname -m) ;; \
        *) echo "Unsupported build architecture: $(uname -m)" >&2; exit 1 ;; \
    esac && \
    export GO_FILENAME=$(curl -sL 'https://go.dev/dl/?mode=json&include=all' | jq -r "[.[] | select(.version == \"${GO_VERSION}\")][0].files[] | select(.os == \"linux\" and .arch == \"${GO_ARCH}\") | .filename") && \
    echo ${GO_FILENAME} && \
    # download and unpack
    curl -sL -o go.tar.gz "https://golang.org/dl/${GO_FILENAME}" && \
    tar -C /usr/local -xzf go.tar.gz && \
    rm go.tar.gz

# add Go to PATH
ENV PATH="/usr/local/go/bin:${PATH}"
RUN go version

# Copy the go source
COPY vendor/ vendor/
COPY version/ version/
COPY cmd/ cmd/
COPY hack/ hack/
COPY api/ api/
COPY internal/ internal/

# for getting version info
COPY .git/ .git/

# Build
RUN ./hack/build.sh

FROM registry.access.redhat.com/ubi9/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
