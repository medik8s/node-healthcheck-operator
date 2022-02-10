# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

# Override this when building images for dev only!
IMAGE_REGISTRY ?= quay.io/medik8s

# VERSION defines the project version for the bundle. 
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
DEFAULT_VERSION := 0.0.1
# Let CI set VERSION based on git tags. But heads up, VERSION should not have the 'v' prefix!
VERSION ?= $(DEFAULT_VERSION)
export VERSION

# use candidate channel
CHANNELS = candidate
export CHANNELS
DEFAULT_CHANNEL = candidate
export DEFAULT_CHANNEL

# CHANNELS define the bundle channels used in the bundle. 
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "preview,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=preview,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="preview,fast,stable")
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle. 
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)


# For the default version, use 'latest' image tags.
# Otherwise version prefixed with 'v'
ifeq ($(VERSION), $(DEFAULT_VERSION))
IMAGE_TAG = latest
else
IMAGE_TAG = v$(VERSION)
endif
export IMAGE_TAG

OPERATOR_NAME ?= node-healthcheck

# For example, running 'make bundle-build bundle-push index-build index-push' will build and push both
# medik8s/node-healthcheck-operator-bundle:$VERSION and medik8s/node-healthcheck-operator-index:$VERSION.
IMAGE_TAG_BASE ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME)

# BUNDLE_IMG defines the image:tag used for the bundle. 
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-operator-bundle:$(IMAGE_TAG)

# INDEX_IMG defines the image:tag used for the index.
# You can use it as an arg. (E.g make bundle-build INDEX_IMG=<some-registry>/<project-name-index>:<tag>)
INDEX_IMG ?= $(IMAGE_TAG_BASE)-operator-index:$(IMAGE_TAG)

# Image URL to use all building/pushing image targets
IMG ?= $(IMAGE_TAG_BASE)-operator:$(IMAGE_TAG)

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true,preserveUnknownFields=false"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Use kubectl, fallback to oc
KUBECTL = kubectl
ifeq (,$(shell which kubectl))
KUBECTL=oc
endif

# CI uses a non-writable home dir, make sure .cache is writable
ifeq ("${HOME}", "/")
HOME=/tmp
endif

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate manifests e.g. CRD, RBAC etc.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations. Also generate protoc / gRPC code
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

fmt: goimports ## Run go fmt against code (skip vendor)
	$(GOIMPORTS) -w ./main.go ./api ./controllers ./e2e

vet: ## Run go vet against code
	go vet ./...

verify: bundle-date-reset ## verify there are no un-committed changes
	./hack/verify-diff.sh

fetch-mutation: ## fetch mutation package.
	GO111MODULE=off go get -t -v github.com/mshitrit/go-mutesting/...

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: test-no-verify ## Generate and format code, run tests, generate manifests and bundle, and verify no uncommitted changes
	VERSION=0.0.1 $(MAKE) manifests bundle verify

test-no-verify: fmt vet generate ## Generate and format code, and run tests
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.7.0/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./controllers/... -coverprofile cover.out -v -ginkgo.v

test-mutation: verify-no-changes fetch-mutation ## Run mutation tests in manual mode.
	echo -e "## Verifying diff ## \n##Mutations tests actually changes the code while running - this is a safeguard in order to be able to easily revert mutation tests changes (in case mutation tests have not completed properly)##"
	./hack/test-mutation.sh

test-mutation-ci: fetch-mutation ## Run mutation tests as part of auto build process.
	./hack/test-mutation.sh

##@ Build

manager: generate fmt vet ## Build manager binary
	./hack/build.sh

run: generate fmt vet manifests ## Run against the configured Kubernetes cluster in ~/.kube/config
	go run ./main.go -leader-elect=false

docker-build: test-no-verify ## Build the docker image; skip linters and verification to not break CI
	podman build -t ${IMG} .

docker-push: ## Push the docker image
	podman push ${IMG}

debug: manager
	dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec bin/manager -- -leader-elect=false

##@ Bundle fixes
# Some fixes in the bundle
.PHONY: bundle-fixes
bundle-fixes: ## update container image
	sed -r -i "s|containerImage: .*|containerImage: $(IMG)|;" ./bundle/manifests/$(OPERATOR_NAME)-operator.clusterserviceversion.yaml

.PHONY: bundle-date
bundle-date: ## Set createdAt date in the bundle's CSV. Do not commit changes!
	sed -r -i "s|createdAt: .*|createdAt: `date '+%Y-%m-%d %T'`|;" ./bundle/manifests/$(OPERATOR_NAME)-operator.clusterserviceversion.yaml

.PHONY: bundle-date-reset
bundle-date-reset: ## Reset createdAt date to empty value
	sed -r -i "s|createdAt: .*|createdAt: \"\"|;" ./bundle/manifests/$(OPERATOR_NAME)-operator.clusterserviceversion.yaml

##@ Download Locally

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.5.0)

KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize: ## Download kustomize locally if necessary
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v3@v3.8.7)


GOIMPORTS = $(shell pwd)/bin/goimports
goimports: ## Download goimports locally if necessary.
	$(call go-get-tool,$(GOIMPORTS),golang.org/x/tools/cmd/goimports@v0.1.6)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

.PHONY: opm
OPM = ./bin/opm
opm: ## Download opm locally if necessary.
ifeq (,$(wildcard $(OPM)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.15.1/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
endif

.PHONY: operator-sdk
OPERATOR_SDK = ./bin/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/v1.15.0/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
endif

##@ Deployment

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete -f -

deploy: manifests kustomize ## Deploy controller in the K8s cluster specified in ~/.kube/config
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build $${KUSTOMIZE_OVERLAY-config/default} | $(KUBECTL) apply -f -

undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete -f -

all: ## Build and push all the three images to quay (docker, bundle, and index).
	docker-build docker-push bundle bundle-build bundle-push index-build index-push

.PHONY: bundle
bundle: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	$(OPERATOR_SDK) generate --verbose kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(OPERATOR_SDK) bundle validate ./bundle
	$(MAKE) bundle-fixes bundle-date

.PHONY: bundle-build
bundle-build: bundle-date ## Build the bundle image.
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image
	podman push ${BUNDLE_IMG}

# Build an index image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: index-build
index-build: opm ## Build an index image.
	$(OPM) index add --container-tool podman --mode semver --tag $(INDEX_IMG) --bundles $(BUNDLE_IMG)

# Push the index image.
.PHONY: index-push
index-push: ## Push an index image.
	podman push $(INDEX_IMG)

PHONY: test-e2e
test-e2e: ## Run end to end tests.
	@test -n "${KUBECONFIG}" -o -r ${HOME}/.kube/config || (echo "Failed to find kubeconfig in ~/.kube/config or no KUBECONFIG set"; exit 1)
	go test ./e2e -coverprofile cover.out -v -timeout 15m

.PHONY: deploy-poison-pill 
PPIL_DIR = $(shell pwd)/testdata/.remediators/poison-pill
PPIL_GIT_REF ?= v0.1.4
PPILL_VERSION ?= 0.1.4
deploy-poison-pill: ## Deploy poison-pill to a running cluster.
	mkdir -p ${PPIL_DIR}
	test -f ${PPIL_DIR}/Makefile || curl -L https://github.com/medik8s/poison-pill/tarball/${PPIL_GIT_REF} | tar -C ${PPIL_DIR} -xzv --strip=1
	# must override IMG because openshift CI overrides IMG as well.
	# must override VERSION because this makefile has VERSION and ppill uses the env variable for substition in the deploy
	$(MAKE) -C ${PPIL_DIR} deploy IMG=quay.io/medik8s/poison-pill-operator:$(PPILL_VERSION) VERSION=$(PPILL_VERSION)