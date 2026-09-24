# Step 1: Extract clean ubi-micro filesystem and RPM database
FROM registry.access.redhat.com/ubi9/ubi-micro:latest AS micro-base

# Step 2: Builder stage
FROM quay.io/konveyor/builder:ubi9-latest AS builder

ARG TARGETOS=linux
ARG TARGETARCH
# Optional RPM packages (e.g., "util-linux-core iproute"). Leave empty for pure Go operators.
ARG EXTRA_PKGS=""

ENV GOPATH=/go \
    GOTOOLCHAIN=auto \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

# Seed /rootfs with ubi-micro files so dnf inspects the existing RPM database
COPY --from=micro-base / /rootfs

# Install optional RPM dependencies into /rootfs on top of micro-base
RUN if [ -n "${EXTRA_PKGS}" ]; then \
        dnf install -y --installroot /rootfs --releasever 9 --setopt=install_weak_deps=0 --nodocs ${EXTRA_PKGS} && \
        dnf clean all --installroot /rootfs && \
        rm -rf /rootfs/var/cache/* /rootfs/var/log/* /rootfs/tmp/* ; \
    fi

WORKDIR /workspace

# Copy source code after dnf install so package layer caching isn't invalidated by code edits
COPY . .

# Prevent Git safe directory errors
RUN git config --global --add safe.directory /workspace

# Build manager binary (go build uses -mod=vendor automatically when vendor/ exists)
RUN ./hack/build.sh

# Step 3: Runtime stage (FROM scratch prevents layer duplication bloat)
FROM scratch

WORKDIR /

# Copy the complete rootfs (ubi-micro + extra pkgs) with zero layer duplication
COPY --from=builder /rootfs /

# Copy application binary
COPY --from=builder /workspace/bin/manager .

USER 65532:65532

ENTRYPOINT ["/manager"]
