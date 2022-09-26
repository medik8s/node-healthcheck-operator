# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

# VERSION defines the project version for the bundle. 
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
DEFAULT_VERSION := 0.0.1
# Let CI set VERSION based on git tags. But heads up, VERSION should not have the 'v' prefix!
VERSION ?= $(DEFAULT_VERSION)
export VERSION

# use candidate for easier upgrades from Tch Preview to GA version. Stable will be the new default though
CHANNELS = candidate,stable
export CHANNELS
DEFAULT_CHANNEL = stable
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

# Override this when building images for dev only!
IMAGE_REGISTRY ?= quay.io/medik8s

# For the default version, use 'latest' image tags.
# Otherwise version prefixed with 'v'
ifeq ($(VERSION), $(DEFAULT_VERSION))
IMAGE_TAG = latest
else
IMAGE_TAG = v$(VERSION)
endif
export IMAGE_TAG

# BUNDLE_IMG defines the image:tag used for the bundle. 
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_REGISTRY)/node-healthcheck-operator-bundle:$(IMAGE_TAG)

# INDEX_IMG defines the image:tag used for the index.
# You can use it as an arg. (E.g make bundle-build INDEX_IMG=<some-registry>/<project-name-index>:<tag>)
INDEX_IMG ?= $(IMAGE_REGISTRY)/node-healthcheck-operator-index:$(IMAGE_TAG)

# Image URL to use all building/pushing image targets
IMG ?= $(IMAGE_REGISTRY)/node-healthcheck-operator:$(IMAGE_TAG)

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.23

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

all: docker-build docker-push bundle bundle-build bundle-push index-build index-push

# CI uses a non-writable home dir, make sure .cache is writable
ifeq ("${HOME}", "/")
HOME=/tmp
endif

# Generate and format code, run tests, generate manifests and bundle, and verify no uncommitted changes
test: test-no-verify
	VERSION=0.0.1 $(MAKE) manifests bundle verify

# Generate and format code, and run tests
test-no-verify: vendor fmt vet generate envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/testbin)" go test ./controllers/... -coverprofile cover.out -v -ginkgo.v

test-mutation: verify-no-changes fetch-mutation ## Run mutation tests in manual mode.
	echo -e "## Verifying diff ## \n##Mutations tests actually changes the code while running - this is a safeguard in order to be able to easily revert mutation tests changes (in case mutation tests have not completed properly)##"
	./hack/test-mutation.sh


# Build manager binary
manager: generate fmt vet
	./hack/build.sh

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go -leader-elect=false

debug: manager
	dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec bin/manager -- -leader-elect=false

# Install CRDs into a cluster
install: manifests kustomize
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests kustomize
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests kustomize
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build $${KUSTOMIZE_OVERLAY-config/default} | $(KUBECTL) apply -f -

# UnDeploy controller from the configured Kubernetes cluster in ~/.kube/config
undeploy:
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code (skip vendor)
fmt: goimports
	$(GOIMPORTS) -w ./main.go ./api ./controllers ./e2e

# Run go vet against code
vet:
	go vet ./...

# Run go mod tidy
tidy:
	go mod tidy

# Run go mod vendor
vendor: tidy
	go mod vendor

verify: bundle-date-reset ## verify there are no un-committed changes
	./hack/verify-diff.sh

fetch-mutation: ## fetch mutation package.
	GO111MODULE=off go get -t -v github.com/mshitrit/go-mutesting/...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build the docker image; skip linters and verification to not break CI
docker-build: test-no-verify
	podman build -t ${IMG} .

# Push the docker image
docker-push:
	podman push ${IMG}

# Download controller-gen locally if necessary
CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen:
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2)

# Download kustomize locally if necessary
KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize:
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v4@v4.5.7)

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20220907012636-c83076e9f792) # no tagged versions :/

.PHONY: operator-sdk
OPERATOR_SDK = ./bin/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/v1.23.0/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
endif

.PHONY: opm
OPM = ./bin/opm
opm: ## Download opm locally if necessary.
ifeq (,$(wildcard $(OPM)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.26.1/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
endif

GOIMPORTS = $(shell pwd)/bin/goimports
goimports: ## Download goimports locally if necessary.
	$(call go-install-tool,$(GOIMPORTS),golang.org/x/tools/cmd/goimports@v0.1.12)

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin GOFLAGS='' go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests kustomize operator-sdk
	$(OPERATOR_SDK) generate --verbose kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(OPERATOR_SDK) bundle validate ./bundle
	$(MAKE) bundle-fixes bundle-date

# Some fixes in the bundle
DEFAULT_ICON_BASE64 := $(shell base64 --wrap=0 ./config/assets/nhc_blue.png)
export ICON_BASE64 ?= ${DEFAULT_ICON_BASE64}
.PHONY: bundle-fixes
bundle-fixes:
    # update container image
	sed -r -i "s|containerImage: .*|containerImage: $(IMG)|;" ./bundle/manifests/node-healthcheck-operator.clusterserviceversion.yaml
	# set icon
	sed -r -i "s|base64data:.*|base64data: ${ICON_BASE64}|;" ./bundle/manifests/node-healthcheck-operator.clusterserviceversion.yaml

# Set createdAt date in the bundle's CSV. Do not commit changes!
.PHONY: bundle-date
bundle-date:
	sed -r -i "s|createdAt: .*|createdAt: `date '+%Y-%m-%d %T'`|;" ./bundle/manifests/node-healthcheck-operator.clusterserviceversion.yaml

# Reset createdAt date to empty value
.PHONY: bundle-date-reset
bundle-date-reset:
	sed -r -i "s|createdAt: .*|createdAt: \"\"|;" ./bundle/manifests/node-healthcheck-operator.clusterserviceversion.yaml

# Build the bundle image.
.PHONY: bundle-build
bundle-build: bundle-date
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

# Push the bundle image
.PHONY: bundle-push
bundle-push:
	podman push ${BUNDLE_IMG}

# Build a index image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: index-build
index-build: opm ## Build a catalog image.
	$(OPM) index add --container-tool podman --mode semver --tag $(INDEX_IMG) --bundles $(BUNDLE_IMG)

# Push the catalog image.
.PHONY: index-push
index-push: ## Push a catalog image.
	podman push $(INDEX_IMG)

# Run end to end tests
.PHONY: test-e2e
test-e2e:
	@test -n "${KUBECONFIG}" -o -r ${HOME}/.kube/config || (echo "Failed to find kubeconfig in ~/.kube/config or no KUBECONFIG set"; exit 1)
	go test ./e2e -coverprofile cover.out -v -timeout 30m

# Deploy self node remediation to a running cluster
.PHONY: deploy-snr
SNR_DIR = $(shell pwd)/testdata/.remediators/snr
SNR_GIT_REF ?= main
SNR_VERSION ?= 0.0.1
deploy-snr:
	mkdir -p ${SNR_DIR}
	test -f ${SNR_DIR}/Makefile || curl -L https://github.com/medik8s/self-node-remediation/tarball/${SNR_GIT_REF} | tar -C ${SNR_DIR} -xzv --strip=1
	$(MAKE) -C ${SNR_DIR} docker-build docker-push deploy VERSION=$(SNR_VERSION)

##@ Targets used by CI

.PHONY: container-build
container-build: ## Build containers
	make docker-build bundle bundle-build

.PHONY: container-push
container-push:  ## Push containers (NOTE: catalog can't be build before bundle was pushed)
	make docker-push bundle-push index-build index-push

.PHONY: test-mutation-ci
test-mutation-ci: fetch-mutation ## Run mutation tests as part of auto build process.
	./hack/test-mutation.sh
