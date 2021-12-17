# Build the manager binary
FROM quay.io/centos/centos:stream8 AS builder
RUN yum install golang -y

# Ensure go 1.16
RUN go install golang.org/dl/go1.16@latest
RUN ~/go/bin/go1.16 download
RUN /bin/cp -f ~/go/bin/go1.16 /usr/bin/go
RUN go version

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY vendor/ vendor/
COPY controllers/ controllers/
COPY metrics/ metrics/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

FROM registry.access.redhat.com/ubi8/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
