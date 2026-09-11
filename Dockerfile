# Build the manager binary
FROM quay.io/konveyor/builder:ubi9-latest AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.sum ./

# Set GOTOOLCHAIN to auto to allow Go to download newer versions
# Set to local to avoid downloading newer versions of Go
ENV GOTOOLCHAIN=auto

# Copy the go source
COPY vendor/ vendor/
COPY version/ version/
COPY cmd/ cmd/
COPY hack/ hack/
COPY api/ api/
COPY internal/ internal/

# for getting version info
COPY .git/ .git/

RUN go version

RUN git config --global --add safe.directory /workspace
RUN ./hack/build.sh

FROM registry.access.redhat.com/ubi9/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
