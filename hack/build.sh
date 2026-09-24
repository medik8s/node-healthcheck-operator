#!/bin/bash -ex
go version

GIT_VERSION=$(git describe --always --tags || true)
VERSION=${CI_VERSION:-${GIT_VERSION}}
GIT_COMMIT=$(git rev-list -1 HEAD || true)
COMMIT=${CI_COMMIT:-${GIT_COMMIT}}
BUILD_DATE=$(date --utc -Iseconds)

mkdir -p bin

# Allow override for debugging flags (defaults to stripping symbols)
LDFLAGS_DEBUG="${LDFLAGS_DEBUG:- -s -w}"

LDFLAGS_VALUE="-X github.com/medik8s/node-healthcheck-operator/v5/version.Version=${VERSION} "
LDFLAGS_VALUE+="-X github.com/medik8s/node-healthcheck-operator/v5/version.GitCommit=${COMMIT} "
LDFLAGS_VALUE+="-X github.com/medik8s/node-healthcheck-operator/v5/version.BuildDate=${BUILD_DATE} "
LDFLAGS_VALUE+="${LDFLAGS_DEBUG}"

# Allow override and use zero by default (static linking)
export CGO_ENABLED=${CGO_ENABLED:-0}
echo "cgo: ${CGO_ENABLED}"

# Export in case it was set
export GOEXPERIMENT="${GOEXPERIMENT}"

# Detect target arch from Go toolchain, default to amd64
GOARCH=$(go env GOARCH)

echo "Building bin/manager with ldflags: ${LDFLAGS_VALUE}"

GOOS=linux GOARCH=${GOARCH:-amd64} go build -ldflags "${LDFLAGS_VALUE}" -o bin/manager cmd/main.go
