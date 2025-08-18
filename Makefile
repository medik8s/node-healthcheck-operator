# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

# https://pkg.go.dev/github.com/operator-framework/operator-sdk?tab=versions
OPERATOR_SDK_VERSION = v1.33.0
# https://pkg.go.dev/github.com/operator-framework/operator-registry?tab=versions
OPM_VERSION = v1.56.0
# https://pkg.go.dev/sigs.k8s.io/controller-tools?tab=versions
CONTROLLER_GEN_VERSION = v0.17.3
# update for major version updates to KUSTOMIZE_VERSION!
KUSTOMIZE_API_VERSION = v5
# https://pkg.go.dev/sigs.k8s.io/kustomize/kustomize/v5?tab=versions
KUSTOMIZE_VERSION = v5.3.0
# https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest/env?tab=versions
ENVTEST_VERSION = v0.0.0-20250813191507-c7df6d0236ed
# https://pkg.go.dev/golang.org/x/tools/cmd/goimports?tab=versions
GOIMPORTS_VERSION = v0.36.0
# https://pkg.go.dev/github.com/slintes/sort-imports?tab=versions
SORT_IMPORTS_VERSION = v0.3.0
# update for major version updates to YQ_VERSION!
YQ_API_VERSION = v4
YQ_VERSION = v4.47.1

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.32

# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
DEFAULT_VERSION := 0.0.1
# Let CI set VERSION based on git tags. But heads up, VERSION should not have the 'v' prefix!
export VERSION ?= $(DEFAULT_VERSION)
# For the replaces field in the CSV, mandatory to be set for versioned builds! Should also not have the 'v' prefix.
export PREVIOUS_VERSION ?= $(DEFAULT_VERSION)
# Lower bound for the skipRange field in the CSV, should be set to the oldest supported version
export SKIP_RANGE_LOWER ?= "0.1.0"

CHANNELS ?= stable
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

# Image base URL of the console plugin
CONSOLE_PLUGIN_IMAGE_BASE ?= quay.io/medik8s/node-remediation-console

# For the default version, use 'latest' image tags.
# Otherwise version prefixed with 'v'
ifeq ($(VERSION), $(DEFAULT_VERSION))
IMAGE_TAG = latest
CONSOLE_PLUGIN_TAG ?= latest
else
IMAGE_TAG = v$(VERSION)
# always release the console with the same tag as NHC and the other way around!
CONSOLE_PLUGIN_TAG ?= v$(VERSION)
endif
export IMAGE_TAG

# Image URL of the console plugin
CONSOLE_PLUGIN_IMAGE ?= $(CONSOLE_PLUGIN_IMAGE_BASE):$(CONSOLE_PLUGIN_TAG)

# Image URL of the kube-rbac-proxy
RBAC_PROXY_IMAGE ?= quay.io/brancz/kube-rbac-proxy:v0.15.0

# Image URL of the must-gather image, used as related image in the downstream CSV
MUST_GATHER_IMAGE ?= quay.io/medik8s/must-gather:latest

OPERATOR_NAME = node-healthcheck-operator
OPERATOR_NAMESPACE ?= openshift-workload-availability

# BUNDLE_IMG defines the image:tag used for the bundle.
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME)-bundle:$(IMAGE_TAG)

# INDEX_IMG defines the image:tag used for the index.
# You can use it as an arg. (E.g make bundle-build INDEX_IMG=<some-registry>/<project-name-index>:<tag>)
INDEX_IMG ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME)-index:$(IMAGE_TAG)

# Image URL to use all building/pushing image targets
IMG ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME):$(IMAGE_TAG)


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

.PHONY: all
all: container-build-ocp container-push

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

# CI uses a non-writable home dir, make sure .cache is writable
ifeq ("${HOME}", "/")
HOME=/tmp
endif

ifeq "$(NHC_SKIP_TEST)" "true"
test:
	@echo "skipping test target!"
test-no-verify:
	@echo "skipping test-no-verify target!"
else

.PHONY: test
test: test-no-verify ## Generate and format code, run tests, generate manifests and bundle, and verify no uncommitted changes
	$(MAKE) bundle-reset verify

.PHONY: test-no-verify
test-no-verify: vendor generate test-imports fmt vet envtest ## Generate and format code, and run tests
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/testbin)" go test ./controllers/... ./api/... -coverprofile cover.out -v -ginkgo.v
endif

.PHONY: manager
manager: generate fmt vet ## Build manager binary
	./hack/build.sh

.PHONY: run
run: generate fmt vet manifests ## Run against the configured Kubernetes cluster in ~/.kube/config
	go run ./main.go -leader-elect=false

.PHONY: debug
debug: manager
	dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec bin/manager -- -leader-elect=false

.PHONY: install
install: manifests kustomize ## Install CRDs into a cluster
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from a cluster
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller in the configured Kubernetes cluster in ~/.kube/config
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	cd config/optional/console-plugin && $(KUSTOMIZE) edit set image console-plugin=${CONSOLE_PLUGIN_IMAGE}
	$(KUSTOMIZE) build $${KUSTOMIZE_OVERLAY-config/default} | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: ## UnDeploy controller from the configured Kubernetes cluster in ~/.kube/config
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete -f -

.PHONY: manifests
manifests: controller-gen ## Generate manifests e.g. CRD, RBAC etc.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: fmt
fmt: goimports ## Run go fmt against code (skip vendor)
	$(GOIMPORTS) -w ./main.go ./api ./controllers ./e2e

.PHONY: vet
vet: ## Run go vet against code
	go vet ./...

.PHONY: test-imports
test-imports: sort-imports ## Check for sorted imports
	$(SORT_IMPORTS) .

.PHONY: fix-imports
fix-imports: sort-imports ## Sort imports
	$(SORT_IMPORTS) -w .


.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

.PHONY: vendor
vendor: tidy ## Run go mod vendor
	go mod vendor

.PHONY: verify
verify: bundle-reset ## verify there are no un-committed changes
	./hack/verify-diff.sh

.PHONY: generate
generate: controller-gen ## Generate code
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: docker-build
docker-build: test-no-verify ## Build the docker image; skip linters and verification to not break CI
	podman build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push the docker image
	podman push ${IMG}

##@ Build Dependencies

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION))

KUSTOMIZE = $(shell pwd)/bin/kustomize
.PHONY: kustomize
kustomize: ## Download kustomize locally if necessary
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/$(KUSTOMIZE_API_VERSION)@$(KUSTOMIZE_VERSION))

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION))

SORT_IMPORTS = $(shell pwd)/bin/sort-imports
.PHONY: sort-imports
sort-imports: ## Download sort-imports locally if necessary.
	$(call go-install-tool,$(SORT_IMPORTS),github.com/slintes/sort-imports@$(SORT_IMPORTS_VERSION))

YQ = $(shell pwd)/bin/yq
.PHONY: yq
yq: ## Download yq locally if necessary.
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/$(YQ_API_VERSION)@$(YQ_VERSION))

.PHONY: operator-sdk
OPERATOR_SDK = ./bin/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=linux && ARCH=amd64 && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$${OS}_$${ARCH} ;\
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
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
endif

GOIMPORTS = $(shell pwd)/bin/goimports
.PHONY: goimports
goimports: ## Download goimports locally if necessary.
	$(call go-install-tool,$(GOIMPORTS),golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION))

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

.PHONY: bundle-base
bundle-base: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	rm -rf ./bundle/manifests
	$(OPERATOR_SDK) generate --verbose kustomize manifests --input-dir ./config/manifests/base --output-dir ./config/manifests/base
	cd config/manifests/base && $(KUSTOMIZE) edit set image controller=$(IMG) && $(KUSTOMIZE) edit set image kube-rbac-proxy=$(RBAC_PROXY_IMAGE)
	cd config/optional/console-plugin && $(KUSTOMIZE) edit set image console-plugin=${CONSOLE_PLUGIN_IMAGE}
	$(KUSTOMIZE) build config/manifests/base | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(MAKE) bundle-validate

export CSV="./bundle/manifests/$(OPERATOR_NAME).clusterserviceversion.yaml"

.PHONY: ocp-version-check
ocp-version-check: ## Check if OCP_VERSION is set
	@if [ -z "${OCP_VERSION}" ]; then \
		echo "Error: OCP_VERSION must be set for this build"; \
		exit 1; \
	fi

.PHONY: add-console-plugin-annotation
add-console-plugin-annotation: ## Add console-plugin annotation to the CSV
	$(YQ) -i '.metadata.annotations."console.openshift.io/plugins" = "[\"node-remediation-console-plugin\"]"' ${CSV}

.PHONY: add-replaces-field
add-replaces-field: ## Add replaces field to the CSV
	# add replaces field when building versioned bundle
	@if [ $(VERSION) != $(DEFAULT_VERSION) ]; then \
		if [ $(PREVIOUS_VERSION) == $(DEFAULT_VERSION) ]; then \
			echo "Error: PREVIOUS_VERSION must be set for versioned builds"; \
			exit 1; \
		else \
		  	# preferring sed here, in order to have "replaces" near "version" \
			sed -r -i "/  version: $(VERSION)/ a\  replaces: $(OPERATOR_NAME).v$(PREVIOUS_VERSION)" ${CSV}; \
		fi \
	fi

.PHONY: add-community-edition-to-display-name
add-community-edition-to-display-name: ## Add the "Community Edition" suffix to the display name
	sed -r -i "s|displayName: Node Health Check Operator|displayName: Node Health Check Operator - Community Edition|;" ${CSV}

.PHONY: bundle-okd
bundle-okd: ocp-version-check yq bundle-base ## Generate bundle manifests and metadata for OKD, then validate generated files.
	$(KUSTOMIZE) build config/manifests/okd | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(MAKE) add-console-plugin-annotation
	$(MAKE) add-replaces-field
	$(MAKE) add-community-edition-to-display-name
	echo -e "\n  # Annotations for OCP\n  com.redhat.openshift.versions: \"v${OCP_VERSION}\"" >> bundle/metadata/annotations.yaml
	ICON_BASE64="$(shell base64 --wrap=0 ./config/assets/nhc_blue.png)" \
		$(MAKE) bundle-update

.PHONY: bundle-ocp
bundle-ocp: yq bundle-base ## Generate bundle manifests and metadata for OCP, then validate generated files.
	$(shell rm -r bundle) # OCP bundle should be created from scratch
	$(KUSTOMIZE) build config/manifests/ocp | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	sed -r -i "s|DOCS_RHWA_VERSION|${DOCS_RHWA_VERSION}|g;" "${CSV}"
	# Add env var with must gather image to the NHC container, so its pullspec gets added to the relatedImages section by OSBS
	#   https://osbs.readthedocs.io/en/osbs_ocp3/users.html?#pinning-pullspecs-for-related-images
	$(YQ) -i '( .spec.install.spec.deployments[0].spec.template.spec.containers[] | select(.name == "manager") | .env) += [{"name": "RELATED_IMAGE_MUST_GATHER", "value": "${MUST_GATHER_IMAGE}"}]' ${CSV}
	$(MAKE) add-console-plugin-annotation
	# add OCP annotations
	$(YQ) -i '.metadata.annotations."operators.openshift.io/valid-subscription" = "[\"OpenShift Kubernetes Engine\", \"OpenShift Container Platform\", \"OpenShift Platform Plus\"]"' ${CSV}
	# new infrastructure annotations see https://docs.engineering.redhat.com/display/CFC/Best_Practices#Best_Practices-(New)RequiredInfrastructureAnnotations
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/disconnected" = "true"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/fips-compliant" = "true"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/proxy-aware" = "false"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/tls-profiles" = "false"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/token-auth-aws" = "false"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/token-auth-azure" = "false"' ${CSV}
	$(YQ) -i '.metadata.annotations."features.operators.openshift.io/token-auth-gcp" = "false"' ${CSV}
	$(MAKE) add-replaces-field
	ICON_BASE64="$(shell base64 --wrap=0 ./config/assets/nhc_red.png)" \
		$(MAKE) bundle-update

.PHONY: bundle-ocp-ci
bundle-ocp-ci: yq ## Generate OCP bundle for CI, without overriding the image pull-spec (CI only)
	IMG="$(shell $(YQ) -r '.metadata.annotations.containerImage' ${CSV})" \
		$(MAKE) bundle-ocp

.PHONY: bundle-k8s
bundle-k8s: bundle-base ## Generate bundle manifests and metadata for K8s community, then validate generated files.
	$(KUSTOMIZE) build config/manifests/k8s | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)

	$(MAKE) add-community-edition-to-display-name
	$(MAKE) bundle-validate

.PHONY: bundle-metrics
bundle-metrics: bundle-base ## Generate bundle manifests and metadata with metric relates manifests, then validate generated files.
	$(KUSTOMIZE) build config/manifests/metrics | $(OPERATOR_SDK) generate --verbose bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(MAKE) bundle-validate

# Apply version or build date related changes in the bundle
DEFAULT_ICON_BASE64 := $(shell base64 --wrap=0 ./config/assets/nhc_blue.png)
export ICON_BASE64 ?= ${DEFAULT_ICON_BASE64}
.PHONY: bundle-update
bundle-update: ## update container image in the metadata
	sed -r -i "s|containerImage: .*|containerImage: $(IMG)|;" ${CSV}
	# set skipRange
	sed -r -i "s|olm.skipRange: .*|olm.skipRange: '>=${SKIP_RANGE_LOWER} <${VERSION}'|;" ${CSV}
	# set icon (not version or build date related, but just to not having this huge data permanently in the CSV)
	sed -r -i "s|base64data:.*|base64data: ${ICON_BASE64}|;" ${CSV}
	$(MAKE) bundle-validate

.PHONY: bundle-validate
bundle-validate: operator-sdk ## Validate the bundle directory with additional validators (suite=operatorframework), such as Kubernetes deprecated APIs (https://kubernetes.io/docs/reference/using-api/deprecation-guide/) based on bundle.CSV.Spec.MinKubeVersion
	$(OPERATOR_SDK) bundle validate ./bundle --select-optional suite=operatorframework

.PHONY: bundle-scorecard
bundle-scorecard: operator-sdk ## Run scorecard tests
	$(OPERATOR_SDK) scorecard ./bundle

.PHONY: bundle-reset
bundle-reset: ## Revert all version or build date related changes
	VERSION=0.0.1 $(MAKE) manifests bundle-k8s
	# empty creation date
	sed -r -i "s|createdAt: .*|createdAt: \"\"|;" ${CSV}
	# delete replaces field
	sed -r -i "/replaces:.*/d" ${CSV}

.PHONY: bundle-build-ocp
bundle-build-ocp: bundle-ocp bundle-update ## Build the bundle image for OCP.
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-build-k8s
bundle-build-k8s: bundle-k8s bundle-update ## Build the bundle image for k8s.
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-build-metrics
bundle-build-metrics: bundle-metrics bundle-update ## Build the bundle image for K8s with metric related configuration
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image
	podman push ${BUNDLE_IMG}

.PHONY: bundle-run
bundle-run: operator-sdk create-ns ## Run bundle image
	$(OPERATOR_SDK) -n $(OPERATOR_NAMESPACE) run bundle $(BUNDLE_IMG)

.PHONY: bundle-cleanup
bundle-cleanup: operator-sdk ## Remove bundle installed via bundle-run
	$(OPERATOR_SDK) -n $(OPERATOR_NAMESPACE) cleanup $(OPERATOR_NAME) --delete-all

.PHONY: create-ns
create-ns: ## Create namespace
	$(KUBECTL) get ns $(OPERATOR_NAMESPACE) 2>&1> /dev/null || $(KUBECTL) create ns $(OPERATOR_NAMESPACE)

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

.PHONY: test-e2e
test-e2e: ## Run end to end tests
	./hack/test-e2e.sh


.PHONY: deploy-snr ## Deploy self node remediation to a running cluster
SNR_DIR = $(shell pwd)/testdata/.remediators/snr
SNR_GIT_REF ?= main
SNR_VERSION ?= 0.0.1
deploy-snr:
	mkdir -p ${SNR_DIR}
	test -f ${SNR_DIR}/Makefile || curl -L https://github.com/medik8s/self-node-remediation/tarball/${SNR_GIT_REF} | tar -C ${SNR_DIR} -xzv --strip=1
	$(MAKE) -C ${SNR_DIR} docker-build docker-push deploy VERSION=$(SNR_VERSION)

##@ Targets used by CI

.PHONY: container-build-ocp
container-build-ocp: ## Build containers for OCP
	make docker-build bundle-build-ocp

.PHONY: container-build-k8s
container-build-k8s: ## Build containers for K8s
	make docker-build bundle-build-k8s

.PHONY: container-build-metrics
container-build-metrics: ## Build containers for K8s with metric related configuration
	make docker-build bundle-build-metrics


.PHONY: container-push
container-push:  ## Push containers (NOTE: catalog can't be build before bundle was pushed)
	make docker-push bundle-push index-build index-push

.PHONY: build-and-run
build-and-run: container-build-ocp container-push bundle-run
