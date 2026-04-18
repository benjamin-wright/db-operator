# Cluster name — matches the name in k3d-config.yaml
CLUSTER_NAME     ?= db-operator

# Directory and path for the kubeconfig written during cluster-up
KUBECONFIG_DIR   ?= $(HOME)/.scratch
KUBECONFIG_PATH  ?= $(KUBECONFIG_DIR)/$(CLUSTER_NAME).yaml

# Registry settings
REGISTRY_NAME    ?= $(CLUSTER_NAME)-registry.localhost
REGISTRY_PORT    ?= 5001

# Image URL to use for all building/pushing image targets
IMG ?= db-operator:latest

# Root of the repository (used to locate shared Dockerfiles)
REPO_ROOT ?= $(shell git rev-parse --show-toplevel 2>/dev/null || echo $(abspath $(dir $(firstword $(MAKEFILE_LIST)))/../../..))

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

# controller-gen is invoked via go run so no local install is required.
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@latest

.PHONY: generate
generate: ## Generate DeepCopy methods and CRD manifests via controller-gen.
	$(CONTROLLER_GEN) object:headerFile="" paths="./pkg/api/..."
	$(CONTROLLER_GEN) crd paths="./pkg/api/..." output:crd:artifacts:config=charts/db-operator/crds

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run unit tests.
	go test ./... -v

# GINKGO invokes the Ginkgo CLI via go run, pinned to the version in go.mod.
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

.PHONY: integration-test
integration-test: fmt vet ## Run all integration tests (requires a running k3d cluster).
	$(GINKGO) -p -v --tags=integration ./internal/migrations/... ./internal/operator/controller/

.PHONY: integration-test-migrations
integration-test-migrations: ## Run migration integration tests.
	$(GINKGO) -p -v --silence-skips --tags=integration ./internal/migrations/...

.PHONY: integration-test-postgres
integration-test-postgres: ## Run Postgres controller integration tests.
	$(GINKGO) -p -v --silence-skips --tags=integration --focus="Postgres" ./internal/operator/controller/

.PHONY: integration-test-redis
integration-test-redis: ## Run Redis controller integration tests.
	$(GINKGO) -p -v --silence-skips --tags=integration --focus="Redis" ./internal/operator/controller/

.PHONY: integration-test-nats
integration-test-nats: ## Run NATS controller integration tests.
	$(GINKGO) -p -v --silence-skips --tags=integration --focus="Nats" ./internal/operator/controller/

##@ Cluster

.PHONY: cluster-up
cluster-up: ## Create the local k3d cluster and registry, then write kubeconfig to KUBECONFIG_PATH.
	@echo "Creating kubeconfig directory $(KUBECONFIG_DIR) …"
	@mkdir -p "$(KUBECONFIG_DIR)"
	@echo "Creating k3d cluster '$(CLUSTER_NAME)' …"
	k3d cluster create $(CLUSTER_NAME) \
		--registry-create $(REGISTRY_NAME):0.0.0.0:$(REGISTRY_PORT) \
		--kubeconfig-update-default=false \
		-p "80:80@loadbalancer" \
		--wait;
	@echo "Writing kubeconfig to $(KUBECONFIG_PATH) …"
	k3d kubeconfig get "$(CLUSTER_NAME)" > "$(KUBECONFIG_PATH)"
	@echo ""
	@echo "Cluster is ready. Run the following (or use direnv) to target it:"
	@echo "  export KUBECONFIG=$(KUBECONFIG_PATH)"
	@echo ""
	@KUBECONFIG="$(KUBECONFIG_PATH)" kubectl get nodes

.PHONY: cluster-down
cluster-down: ## Tear down the local k3d cluster and registry.
	@echo "Deleting k3d cluster '$(CLUSTER_NAME)' …"
	k3d cluster delete "$(CLUSTER_NAME)"
	@echo "Cluster '$(CLUSTER_NAME)' deleted."
