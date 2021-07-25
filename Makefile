# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

# VERSION defines the project version for the bundle. 
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
VERSION ?= $(shell git describe --match="v*"| sed 's/v//')

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

# BUNDLE_IMG defines the image:tag used for the bundle. 
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= quay.io/medik8s/node-healthcheck-operator-bundle:$(VERSION)

# Image URL to use all building/pushing image targets
IMG ?= quay.io/medik8s/node-healthcheck-operator:$(VERSION)

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

all: manager

# Run tests
ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: generate fmt vet manifests
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.7.0/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); go test ./controllers -coverprofile cover.out -v -ginkgo.v

test-mutation: verify-no-changes fetch-mutation ## Run mutation tests in manual mode.
	echo -e "## Verifying diff ## \n##Mutations tests actually changes the code while running - this is a safeguard in order to be able to easily revert mutation tests changes (in case mutation tests have not completed properly)##"
	./hack/test-mutation.sh

test-mutation-ci: fetch-mutation ## Run mutation tests as part of auto build process.
	./hack/test-mutation.sh

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go -leader-elect=false

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
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

verify-no-changes: ## verify no there are no un-staged changes
	./hack/verify-diff.sh

fetch-mutation: ## fetch mutation package.
	GO111MODULE=off go get -t -v github.com/mshitrit/go-mutesting/...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build the docker image
docker-build: test
	podman build -t ${IMG} .

# Push the docker image
docker-push:
	podman push ${IMG}
# Download controller-gen locally if necessary
CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen:
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.5.0)

# Download kustomize locally if necessary
KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize:
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v3@v3.8.7)

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

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests kustomize
	operator-sdk generate --verbose kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | operator-sdk generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate ./bundle

# Build the bundle image.
.PHONY: bundle-build
bundle-build:
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

# Push the bundle image
.PHONY: bundle-push
bundle-push:
	podman push ${BUNDLE_IMG}

# Run end to end tests
PHONY: test-e2e
test-e2e:
	@test -n "${KUBECONFIG}" -o -r ${HOME}/.kube/config || (echo "Failed to find kubeocnfig in ~/.kube/config or no KUBECONFIG set"; exit 1)
	#TODO not sure needed @if [ -z ${PPIL_IMG} ]; then echo "[WARN] PPIL_IMG is not set"; fi
	$(MAKE) deploy KUSTOMIZE_OVERLAY=config/overlays/e2e
	$(MAKE) deploy-poison-pill
	go test ./e2e -coverprofile cover.out -v -timeout 15m

# Deploy poison-pill to a running cluster
.PHONY: deploy-poison-pill
PPIL_DIR = $(shell pwd)/testdata/.remediators/poison-pill
PPIL_GIT_REF ?= ab2fe35
PPILL_VERSION ?= 0.1.2-11-gab2fe35
deploy-poison-pill:
	mkdir -p ${PPIL_DIR}
	test -f ${PPIL_DIR}/Makefile || curl -L https://github.com/medik8s/poison-pill/tarball/${PPIL_GIT_REF} | tar -C ${PPIL_DIR} -xzv --strip=1
	# must override IMG because openshift CI overrides IMG as well.
	# must override VERSION because this makefile has VERSION and ppill uses the env variable for substition in the deploy
	$(MAKE) -C ${PPIL_DIR} deploy IMG=quay.io/medik8s/poison-pill-operator:$(PPILL_VERSION) VERSION=$(PPILL_VERSION)

docker-push-latest: IMG_ORIG:=$(IMG)
docker-push-latest: VERSION=$(shell git describe --abbrev=0 | sed 's/v//')-latest
docker-push-latest:
	podman tag $(IMG_ORIG) $(IMG)
	$(MAKE) docker-push IMG=$(IMG)

