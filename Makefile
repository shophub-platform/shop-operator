# Image URL to use all building/pushing image targets
IMG ?= docker.io/shophubplatform/shop-operator:latest

# Get the currently used golang install path
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

CONTAINER_TOOL ?= docker

.PHONY: all
all: build

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRDs.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: fmt vet ## Run unit tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: fmt vet ## Run the controller locally.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build the operator container image.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push the operator container image.
	$(CONTAINER_TOOL) push ${IMG}

##@ Deployment

.PHONY: install
install: ## Install CRDs into the cluster.
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall: ## Remove CRDs from the cluster.
	kubectl delete --ignore-not-found -f config/crd/bases

.PHONY: samples
samples: ## Apply sample CRs.
	kubectl apply -f config/samples

##@ Dependencies

CONTROLLER_GEN ?= $(GOBIN)/controller-gen
.PHONY: controller-gen
controller-gen: ## Install controller-gen if missing.
	test -s $(CONTROLLER_GEN) || GOBIN=$(GOBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0
