#!/bin/bash
# Copy of downstream render_templates.sh to test the bundle build locally.
set -eux

####################################################
### UPDATE VARS IN THIS SECTION FOR EACH RELEASE ###
####################################################

# For the docs URL
export DOCS_RHWA_VERSION="24.1"
# For the rbac proxy image; only works after release, so it's one release behind most times...
export RBAC_PROXY_OCP_VERSION="4.14"

# Bundle channels
# !!! keep aligned with the channels in the Dockerfile.in !!!!
#CHANNELS="4.14-eus,stable"
export CHANNELS="stable"

# skip range lower boundary, set to last eus version
# 0.6.x = 4.14-eus
export SKIP_RANGE_LOWER="0.6.0"
# For the replaces field in the CSV, set to the last released version
# Prevents removal of that version from index
export PREVIOUS_VERSION="0.6.1"

##################
### Fixes vars ###
##################

export BUILD_REGISTRY="registry-proxy.engineering.redhat.com/rh-osbs/red-hat-workload-availability"
export OPERATOR_NAME="node-healthcheck-operator"
export CONSOLE_OPERATOR_NAME="node-remediation-console"
export MUST_GATHER_NAME="node-healthcheck-must-gather-rhel8"
export ANNOTATIONS="bundle/metadata/annotations.yaml"

# cd into project dir
cd "../.."

# Override Makefile variables
export VERSION=${CI_VERSION}
export IMG=${BUILD_REGISTRY}-${OPERATOR_NAME}:v${CI_VERSION}
export CONSOLE_PLUGIN_IMAGE=${BUILD_REGISTRY}-${CONSOLE_OPERATOR_NAME}:v${CI_VERSION}
export RBAC_PROXY_IMAGE="registry.redhat.io/openshift4/ose-kube-rbac-proxy:v${RBAC_PROXY_OCP_VERSION}"
export MUST_GATHER_IMAGE=${BUILD_REGISTRY}-${MUST_GATHER_NAME}:v${CI_VERSION}

# Make bundle
rm -rf bundle
make bundle-ocp
