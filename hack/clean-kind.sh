#!/usr/bin/env bash
# clean-kind.sh — remove a local Medik8s Kind cluster and its host artifacts.
#
# This removes only the named cluster, its local registry container/volumes,
# image tags using that registry, and the matching kubeconfig entries.
# Repository checkouts such as .snr/ and .tools/ are intentionally preserved.
#
# Usage:
#   ./hack/clean-kind.sh [options]
#
# Options:
#   --name NAME             Kind cluster name (default: $MEDIK8S_CLUSTER_NAME or medik8s-ci)
#   --registry-name NAME    Registry container name (default: $MEDIK8S_REGISTRY_NAME or kind-registry)
#   --registry-port PORT    Registry port (default: $MEDIK8S_REGISTRY_PORT or 5000)
#   --container-tool TOOL   docker or podman (default: auto-detect)
#   --keep-images           Keep host images tagged with the local registry name
#   --keep-kubeconfig       Keep the kind-<name> kubeconfig entries
#   --remove-network        Remove the shared 'kind' network when it is unused
#   --dry-run               Print actions without changing anything
#   --yes                   Do not prompt for confirmation
#   -h, --help              Show this help
#   EXAMPLE: ./hack/clean-kind.sh --yes --remove-network

set -euo pipefail

CLUSTER_NAME="${MEDIK8S_CLUSTER_NAME:-medik8s-ci}"
REGISTRY_NAME="${MEDIK8S_REGISTRY_NAME:-kind-registry}"
REGISTRY_PORT="${MEDIK8S_REGISTRY_PORT:-5000}"
CONTAINER_TOOL="${CONTAINER_TOOL:-}"
KEEP_IMAGES=false
KEEP_KUBECONFIG=false
REMOVE_NETWORK=false
DRY_RUN=false
ASSUME_YES=false

usage() {
    sed -n '1,35p' "$0"
}

die() {
    echo "Error: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            [[ $# -ge 2 ]] || die "--name requires a value"
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --registry-name)
            [[ $# -ge 2 ]] || die "--registry-name requires a value"
            REGISTRY_NAME="$2"
            shift 2
            ;;
        --registry-port)
            [[ $# -ge 2 ]] || die "--registry-port requires a value"
            REGISTRY_PORT="$2"
            shift 2
            ;;
        --container-tool)
            [[ $# -ge 2 ]] || die "--container-tool requires a value"
            CONTAINER_TOOL="$2"
            shift 2
            ;;
        --keep-images)
            KEEP_IMAGES=true
            shift
            ;;
        --keep-kubeconfig)
            KEEP_KUBECONFIG=true
            shift
            ;;
        --remove-network)
            REMOVE_NETWORK=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --yes)
            ASSUME_YES=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1"
            ;;
    esac
done

command -v kind >/dev/null 2>&1 || die "kind is not installed"

if [[ -z "${CONTAINER_TOOL}" ]]; then
    if command -v podman >/dev/null 2>&1; then
        CONTAINER_TOOL=podman
    elif command -v docker >/dev/null 2>&1; then
        CONTAINER_TOOL=docker
    else
        die "neither podman nor docker is installed"
    fi
fi

command -v "${CONTAINER_TOOL}" >/dev/null 2>&1 || die "container tool not found: ${CONTAINER_TOOL}"

KIND_CONTEXT="kind-${CLUSTER_NAME}"
REGISTRY_IMAGE_PREFIX="${REGISTRY_NAME}:${REGISTRY_PORT}/"
LOCALHOST_IMAGE_PREFIX="localhost:${REGISTRY_PORT}/"

if [[ "${ASSUME_YES}" != true && "${DRY_RUN}" != true ]]; then
    cat <<EOF
This will remove:
  Kind cluster:       ${CLUSTER_NAME}
  Registry container: ${REGISTRY_NAME}
  Registry volumes owned by that container
  Host images under:  ${REGISTRY_IMAGE_PREFIX} and ${LOCALHOST_IMAGE_PREFIX}
  Kubeconfig entries: ${KIND_CONTEXT}

It will not remove repository files, .snr/, .tools/, or unrelated clusters.
EOF
    read -r -p "Continue? [y/N] " answer
    [[ "${answer}" =~ ^[Yy]$ ]] || { echo "Cancelled."; exit 0; }
fi

run() {
    if [[ "${DRY_RUN}" == true ]]; then
        printf '+ '
        printf '%q ' "$@"
        printf '\n'
    else
        "$@"
    fi
}

echo "=== Cleaning local Kind environment ==="
echo "Cluster: ${CLUSTER_NAME}"
echo "Registry: ${REGISTRY_NAME}:${REGISTRY_PORT}"
echo "Container tool: ${CONTAINER_TOOL}"

export KIND_EXPERIMENTAL_PROVIDER="${CONTAINER_TOOL}"

# Capture registry volumes before removing the container. Anonymous registry
# volumes otherwise survive `container rm` and retain the old image store.
registry_volumes=()
if "${CONTAINER_TOOL}" inspect "${REGISTRY_NAME}" >/dev/null 2>&1; then
    while IFS= read -r volume; do
        [[ -n "${volume}" ]] && registry_volumes+=("${volume}")
    done < <("${CONTAINER_TOOL}" inspect "${REGISTRY_NAME}" --format '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{"\n"}}{{end}}{{end}}')
fi

if kind get clusters 2>/dev/null | grep -Fxq "${CLUSTER_NAME}"; then
    echo "=== Deleting Kind cluster '${CLUSTER_NAME}' ==="
    run kind delete cluster --name "${CLUSTER_NAME}"
else
    if command -v kubectl >/dev/null 2>&1 \
        && kubectl config get-contexts -o name 2>/dev/null | grep -Fxq "${KIND_CONTEXT}" \
        && kubectl --context "${KIND_CONTEXT}" cluster-info >/dev/null 2>&1; then
        die "cluster '${CLUSTER_NAME}' is reachable but not visible to the current Kind provider; use the same container tool/user that created it"
    fi
    echo "Kind cluster '${CLUSTER_NAME}' is not visible to the current Kind provider."
fi

if "${CONTAINER_TOOL}" inspect "${REGISTRY_NAME}" >/dev/null 2>&1; then
    echo "=== Removing registry container '${REGISTRY_NAME}' ==="
    run "${CONTAINER_TOOL}" rm -f "${REGISTRY_NAME}"
fi

for volume in "${registry_volumes[@]}"; do
    if "${CONTAINER_TOOL}" volume inspect "${volume}" >/dev/null 2>&1; then
        echo "=== Removing registry volume '${volume}' ==="
        run "${CONTAINER_TOOL}" volume rm -f "${volume}"
    fi
done

if [[ "${KEEP_IMAGES}" != true ]]; then
    mapfile -t registry_images < <(
        "${CONTAINER_TOOL}" images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null \
            | awk -v a="${REGISTRY_IMAGE_PREFIX}" -v b="${LOCALHOST_IMAGE_PREFIX}" \
                'index($0, a) == 1 || index($0, b) == 1'
    )
    for image in "${registry_images[@]}"; do
        [[ -n "${image}" ]] || continue
        echo "=== Removing host image '${image}' ==="
        run "${CONTAINER_TOOL}" image rm -f "${image}"
    done
else
    echo "Keeping host images (--keep-images)."
fi

if [[ "${KEEP_KUBECONFIG}" != true ]] && command -v kubectl >/dev/null 2>&1; then
    if kubectl config get-contexts -o name 2>/dev/null | grep -Fxq "${KIND_CONTEXT}"; then
        echo "=== Removing kubeconfig context '${KIND_CONTEXT}' ==="
        run kubectl config delete-context "${KIND_CONTEXT}"
    fi
    if kubectl config get-clusters 2>/dev/null | grep -Fxq "${KIND_CONTEXT}"; then
        echo "=== Removing kubeconfig cluster '${KIND_CONTEXT}' ==="
        run kubectl config delete-cluster "${KIND_CONTEXT}"
    fi
    if kubectl config view -o jsonpath='{.users[*].name}' 2>/dev/null | tr ' ' '\n' | grep -Fxq "${KIND_CONTEXT}"; then
        echo "=== Removing kubeconfig user '${KIND_CONTEXT}' ==="
        run kubectl config unset "users.${KIND_CONTEXT}"
    fi
else
    echo "Keeping kubeconfig entries (--keep-kubeconfig)."
fi

if [[ "${REMOVE_NETWORK}" == true ]]; then
    if kind get clusters 2>/dev/null | grep -q .; then
        echo "Keeping shared 'kind' network because another Kind cluster exists."
    elif "${CONTAINER_TOOL}" network inspect kind >/dev/null 2>&1 \
        && [[ -z $("${CONTAINER_TOOL}" ps -a --filter network=kind --format '{{.Names}}') ]]; then
        echo "=== Removing unused shared 'kind' network ==="
        run "${CONTAINER_TOOL}" network rm kind
    else
        echo "Keeping 'kind' network because it is missing or still in use."
    fi
fi

echo "=== Kind cleanup complete ==="
