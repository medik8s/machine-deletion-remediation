# SHELL defines bash so all the inline scripts here will work as expected.
SHELL := /bin/bash

OPERATOR_NAME := machine-deletion-remediation

## Tool versions
# OPERATOR_SDK versions at https://github.com/operator-framework/operator-sdk/releases
OPERATOR_SDK_VERSION ?= v1.32.0

# OPM versions at https://github.com/operator-framework/operator-registry/releases
OPM_VERSION = v1.36.0

# CONTROLLER_GEN versions at https://github.com/kubernetes-sigs/controller-tools/releases
CONTROLLER_GEN_VERSION = v0.14.0

# KUSTOMIZE versions at https://github.com/kubernetes-sigs/kustomize/releases
# note: update KUSTOMIZE_VERSION and KUSTOMIZE_API_VERSION accordingly.
KUSTOMIZE_API_VERSION = v5
KUSTOMIZE_VERSION = v5.3.0

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.26

# GoImports versions at https://pkg.go.dev/golang.org/x/tools/cmd/goimports?tab=versions
GOIMPORTS_VERSION ?= v0.17.0

# Sort-imports versions at https://github.com/slintes/sort-imports/releases
SORT_IMPORTS_VERSION = v0.2.1

# OCP Version: for OKD bundle community
OCP_VERSION = 4.12

# VERSION defines the project version for the bundle. 
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
DEFAULT_VERSION := 0.0.1
VERSION ?= $(DEFAULT_VERSION)
REPLACES_VERSION ?= $(VERSION)
export VERSION

# CHANNELS define the bundle channels used in the bundle.
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "preview,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=preview,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="preview,fast,stable")
CHANNELS ?= stable
export CHANNELS

ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle.
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
DEFAULT_CHANNEL ?= stable
export DEFAULT_CHANNEL

ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# IMAGE_REGISTRY used to indicate the registery/group for the operator and bundle
IMAGE_REGISTRY ?= quay.io/medik8s
export IMAGE_REGISTRY

# When no version is set, use latest as image tags
ifeq ($(VERSION), $(DEFAULT_VERSION))
IMAGE_TAG = latest
else
IMAGE_TAG = v$(VERSION)
endif
export IMAGE_TAG

# IMAGE_TAG_BASE defines the docker.io namespace and part of the image name for remote images.
# This variable is used to construct full image tags for bundle images.
#
# For example, running 'make bundle-build bundle-push' will build and push the bundle image
# medik8s/machine-deletion-remediation-bundle:$(IMAGE_TAG) and medik8s/machine-deletion-remediation-catalog:$(IMAGE_TAG).
IMAGE_TAG_BASE ?= $(IMAGE_REGISTRY)/$(OPERATOR_NAME)

# BUNDLE_IMG defines the image:tag used for the bundle.
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-operator-bundle:$(IMAGE_TAG)

# Image URL to use all building/pushing image targets
export IMG ?= $(IMAGE_TAG_BASE)-operator:$(IMAGE_TAG)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: all
all: manager

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

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate manifests e.g. CRD, RBAC etc.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: goimports ## Run go goimports against code - goimports = go fmt + fixing imports.
	$(GOIMPORTS) -w  ./main.go ./api ./controllers ./e2e

.PHONY: vet
vet: ## Run go vet against code
	go vet ./...

.PHONY: go-tidy
go-tidy: # Run go mod tidy - add missing and remove unused modules.
	go mod tidy

.PHONY: go-vendor
go-vendor:  # Run go mod vendor - make vendored copy of dependencies.
	go mod vendor

.PHONY: go-verify
go-verify: go-tidy go-vendor # Run go mod verify - verify dependencies have expected content
	go mod verify

.PHONY: test-imports
test-imports: sort-imports ## Check for sorted imports
	$(SORT_IMPORTS) .

.PHONY: fix-imports
fix-imports: sort-imports ## Sort imports
	$(SORT_IMPORTS) -w .

.PHONY: verify-no-changes
verify-no-changes: bundle-reset ## verify no there are no un-staged changes and remove unwanted changes to bundle
	./hack/verify-diff.sh

# Run tests
# Use TEST_OPS to pass further options to `go test` (e.g. verbosity and/or -ginkgo.focus)
export TEST_OPS ?= ""
.PHONY: test
test: test-no-verify-changes verify-no-changes ## Run tests and verify no changes

.PHONY: test-no-verify-changes
test-no-verify-changes: manifests generate go-verify fmt vet test-imports envtest ## Generate and format code, run tests, generate manifests and bundle
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path  --bin-dir $(PROJECT_DIR)/testbin)" \
	go test ./controllers/... -coverprofile cover.out ${TEST_OPS}

.PHONY: test-e2e
test-e2e: ## Run end to end tests
	# KUBECONFIG must be set to the cluster, and MDR needs to be deployed already
	@test -n "${KUBECONFIG}" -o -r ${HOME}/.kube/config || (echo "Failed to find kubeconfig in ~/.kube/config or no KUBECONFIG set"; exit 1)
	go test ./e2e -coverprofile cover.out -v -timeout 25m -ginkgo.vv  ${TEST_OPS}

##@ Build

.PHONY: manager
manager: generate fmt vet ## Build manager binary
	./hack/build.sh ./bin

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: docker-build
docker-build: test-no-verify-changes ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from a cluster
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller in the configured Kubernetes cluster in ~/.kube/config
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: ## UnDeploy controller from the configured Kubernetes cluster in ~/.kube/config
	$(KUSTOMIZE) build config/default | kubectl delete -f -


##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: controller-gen
CONTROLLER_GEN = $(LOCALBIN)/controller-gen
controller-gen: ## Download controller-gen locally if necessary
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION))


.PHONY: kustomize
KUSTOMIZE = $(LOCALBIN)/kustomize
kustomize: ## Download kustomize locally if necessary
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/$(KUSTOMIZE_API_VERSION)@$(KUSTOMIZE_VERSION))

.PHONY: envtest
ENVTEST = $(LOCALBIN)/setup-envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest)

.PHONY: goimports
GOIMPORTS = $(LOCALBIN)/goimports
goimports: ## Download goimports locally if necessary.
	$(call go-install-tool,$(GOIMPORTS),golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION))

.PHONY: sort-imports
SORT_IMPORTS = $(LOCALBIN)/sort-imports
sort-imports: ## Download sort-imports locally if necessary.
	$(call go-install-tool,$(SORT_IMPORTS),github.com/slintes/sort-imports@$(SORT_IMPORTS_VERSION))

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin GOFLAGS='' go install $(2) ;\
}
endef

.PHONY: opm
OPM_DIR = $(LOCALBIN)/opm
OPM = $(OPM_DIR)/$(OPM_VERSION)/opm
opm: ## Download opm locally if necessary.
	$(call operator-framework-tool, $(OPM), $(OPM_DIR),https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/$${OS}-$${ARCH}-opm )

.PHONY: operator-sdk
OPERATOR_SDK_DIR ?= $(LOCALBIN)/operator-sdk
OPERATOR_SDK = $(OPERATOR_SDK_DIR)/$(OPERATOR_SDK_VERSION)/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
	$(call operator-framework-tool, $(OPERATOR_SDK), $(OPERATOR_SDK_DIR),github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$${OS}_$${ARCH})

# operator-framework-tool will delete old package $2, then download $3 to $1.
define operator-framework-tool
@[ -f $(1) ] || { \
	set -e ;\
	rm -rf $(2) ;\
	mkdir -p $(dir $(1)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	echo "Downloading $(3)" ;\
	curl -sSLo $(1) $(3) ;\
	chmod +x $(1) ;\
	}
endef

##@ Working with Bundle

.PHONY: bundle
bundle: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	$(OPERATOR_SDK) generate kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | $(OPERATOR_SDK) generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	$(MAKE) bundle-validate

## Some addition to bundle creation in the bundle
DEFAULT_ICON_BASE64 := $(shell base64 --wrap=0 ./config/assets/medik8s_blue_icon.png)
export ICON_BASE64 ?= ${DEFAULT_ICON_BASE64}
export CSV="./bundle/manifests/$(OPERATOR_NAME).clusterserviceversion.yaml"

.PHONY: bundle-update
bundle-update: verify-previous-version ## Update CSV fields and validate the bundle directory
	sed -r -i "s|containerImage: .*|containerImage: $(IMG)|;" ${CSV}
	sed -r -i "s|createdAt: .*|createdAt: \"`date '+%Y-%m-%d %T'`\"|;" ${CSV}
	sed -r -i "s|base64data:.*|base64data: ${ICON_BASE64}|;" ${CSV}
	sed -r -i "s|replaces: .*|replaces: machine-deletion-remediation.v${REPLACES_VERSION}|;" ${CSV}
	$(MAKE) bundle-validate

.PHONY: bundle-reset
bundle-reset: ## Revert all version or build date related changes
	VERSION=${DEFAULT_VERSION} IMG=$(IMAGE_TAG_BASE)-operator:latest; $(MAKE) manifests bundle
	sed -r -i "s|containerImage: .*|containerImage: \"\"|;" ${CSV}
	sed -r -i "s|createdAt: .*|createdAt: \"\"|;" ${CSV}
	sed -r -i "s|base64data:.*|base64data: base64EncodedIcon|;" ${CSV}
	sed -r -i "s|replaces: .*|replaces: machine-deletion-remediation.v${DEFAULT_VERSION}|;" ${CSV}

.PHONY: verify-previous-version
verify-previous-version: ## Verifies that PREVIOUS_VERSION variable is set
	@if [ $(VERSION) != $(DEFAULT_VERSION) ] && [ $(PREVIOUS_VERSION) = $(DEFAULT_VERSION) ]; then \
  			echo "Error: PREVIOUS_VERSION must be set for the selected VERSION"; \
    		exit 1; \
    fi

.PHONY: bundle-community
bundle-community: bundle ## Update displayName field in the bundle's CSV
	sed -r -i "s|displayName: Machine Deletion Remediation operator.*|displayName: Machine Deletion Remediation Operator - Community Edition|;" ${CSV}
	echo -e "\n  # Annotations for OCP\n  com.redhat.openshift.versions: \"v${OCP_VERSION}\"" >> bundle/metadata/annotations.yaml
	$(MAKE) bundle-update

.PHONY: bundle-validate
bundle-validate: operator-sdk ## Validate the bundle directory with additional validators (suite=operatorframework), such as Kubernetes deprecated APIs (https://kubernetes.io/docs/reference/using-api/deprecation-guide/) based on bundle.CSV.Spec.MinKubeVersion
	$(OPERATOR_SDK) bundle validate ./bundle --select-optional suite=operatorframework

.PHONY: bundle-build
bundle-build: bundle bundle-update ## Build the bundle image.
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image.
	$(MAKE) docker-push IMG=$(BUNDLE_IMG)

.PHONY: bundle-run
export BUNDLE_RUN_NAMESPACE ?= openshift-workload-availability
bundle-run: operator-sdk ## Run bundle image
	$(OPERATOR_SDK) -n $(BUNDLE_RUN_NAMESPACE) run bundle $(BUNDLE_IMG)

.PHONY: bundle-cleanup
bundle-cleanup: operator-sdk ## Remove bundle installed via bundle-run
	$(OPERATOR_SDK) -n $(BUNDLE_RUN_NAMESPACE) cleanup $(OPERATOR_NAME)

##@ Targets used by CI

.PHONY: container-build
container-build: ## Build containers
	make docker-build bundle-build

.PHONY: container-push
container-push:  ## Push containers
	make docker-push bundle-push
