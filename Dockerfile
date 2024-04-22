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
    export GO_VERSION=$(grep -E "go [[:digit:]]\.[[:digit:]][[:digit:]]" go.mod | awk '{print $2}') && \
    echo ${GO_VERSION} && \
    # find filename for latest z version from Go download page
    export GO_FILENAME=$(curl -sL 'https://go.dev/dl/?mode=json&include=all' | jq -r "[.[] | select(.version | startswith(\"go${GO_VERSION}\"))][0].files[] | select(.os == \"linux\" and .arch == \"amd64\") | .filename") && \
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
