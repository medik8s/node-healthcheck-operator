#!/bin/bash
# Helper script to test the OCP bundle image without the need to push to Red Hat registry.
set -eux

export DOCS_RHWA_VERSION="24.2"
# Speed up the process by skipping the tests
export NHC_SKIP_TEST="true"

# NOTE: Image registry will be quay.io/<username>. !!Don't use medik8s account!!
IMAGE_REGISTRY=${IMAGE_REGISTRY:-"quay.io/username"}

if [[ ${IMAGE_REGISTRY} =~ "username" ]]; then
  echo "[!] Please replace the IMAGE_REGISTRY with your quay.io username"
  exit 1
fi

if [[ ${IMAGE_REGISTRY} =~ "medik8s" ]]; then
  echo "[!] Do not use medik8s as quay.io username. Replace the IMAGE_REGISTRY with your quay.io username"
  exit 1
fi

# cd into project dir
cd "../.."
rm -rf bundle

# Make bundle image
make container-build-ocp container-push
set +x
echo "Now you can use make bundle-run to test th image"
