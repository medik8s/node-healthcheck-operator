#!/usr/bin/env bash
set -euo pipefail

YQ=${YQ:-yq}
HELM=${HELM:-helm}
NAMESPACE=node-healthcheck-operator-system

tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

"${HELM}" template node-healthcheck dist/chart \
  --namespace "${NAMESPACE}" > "${tmp}/chart.yaml"

normalize='
  [.] | sort_by(.kind, .metadata.name) | .[]
  | select(.kind != "Namespace")
  | del(.metadata.labels."helm.sh/chart")
  | del(.metadata.labels."app.kubernetes.io/instance")
  | del(.metadata.labels."app.kubernetes.io/managed-by")
  | del(.metadata.annotations."helm.sh/resource-policy")
  | del(.spec.template.metadata.labels."helm.sh/chart")
  | del(.spec.template.metadata.labels."app.kubernetes.io/instance")
  | del(.spec.template.metadata.labels."app.kubernetes.io/managed-by")
  | del(.spec.template.spec.containers[].imagePullPolicy)
  | (.spec.template.spec.containers[] | select(has("args")) | .args) |= sort
  | del(.. | select(. == {}) | select(path | .[-1] == "annotations"))
  | (.. | select(tag == "!!str")) style=""
  | sort_keys(..)
  | ... comments=""
  | splitDoc
'

"${YQ}" eval-all "${normalize}" dist/install.yaml > "${tmp}/install.normalized.yaml"
"${YQ}" eval-all "${normalize}" "${tmp}/chart.yaml" > "${tmp}/chart.normalized.yaml"

if ! diff -u "${tmp}/install.normalized.yaml" "${tmp}/chart.normalized.yaml"; then
  echo
  echo "dist/chart does not render the same resources as dist/install.yaml."
  echo "Run 'make helm-chart' and update dist/chart/values.yaml for the differences above."
  exit 1
fi
echo "dist/chart renders the same resources as dist/install.yaml"
