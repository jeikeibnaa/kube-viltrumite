BINARY_OPERATOR := bin/operator
UI_DIR          := ui
# Operator image. config/default deploys ghcr.io/jeikeibnaa/kube-viltrumite:dev.
IMG             ?= ghcr.io/jeikeibnaa/kube-viltrumite:dev

# controller-gen runs pinned via `go run pkg@version` so it works the same on
# Windows, Linux and CI without a GOPATH install and without touching go.mod.
CONTROLLER_TOOLS_VERSION := v0.21.0
CONTROLLER_GEN           := go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
# Keep in sync with the golangci-lint-action `version:` in .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION    := v2.14.0
GOLANGCI_LINT            := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
# envtest: etcd + kube-apiserver for the controller tests that need a real API
# server. setup-envtest has no release tags; this is the head of its
# release-0.23 branch (controller-runtime v0.23). The Kubernetes version is
# that of k8s.io/api in go.mod. The binaries are downloaded once into
# ENVTEST_BIN_DIR (absolute: the tests run in their package directory); point
# it elsewhere to share them between checkouts.
SETUP_ENVTEST_VERSION    := v0.0.0-20260305142021-f9589b9f2b9d
SETUP_ENVTEST            := go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)
ENVTEST_K8S_VERSION      := 1.35.0
ENVTEST_BIN_DIR          ?= $(CURDIR)/bin/envtest

# The `vilt` kubectl plugin (cmd/cli) lands in v0.9.0; until then build is operator-only.

.PHONY: help build ui test envtest vet lint verify generate manifests install run docker-build deploy undeploy e2e clean

help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-13s %s\n", $$1, $$2}'

build: ## Build the operator binary into bin/
	go build -o $(BINARY_OPERATOR) ./cmd/operator

ui: ## Install UI deps from the lockfile and build ui/dist
	cd $(UI_DIR) && npm ci && npm run build

test: ## Run unit tests (the envtest ones skip; see envtest)
	go test ./...

envtest: ## Run the controller tests that need a real API server (downloads etcd + kube-apiserver once)
	@assets="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir "$(ENVTEST_BIN_DIR)" -p path)" && [ -n "$$assets" ] && \
		echo "KUBEBUILDER_ASSETS=$$assets" && \
		KUBEBUILDER_ASSETS="$$assets" go test ./internal/controller/... -count=1 -run '^TestEnvtest' -v

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (pinned, same version as CI; see .golangci.yml)
	$(GOLANGCI_LINT) run ./...

verify: vet test build ## Pre-commit gate: vet + test + build

generate: manifests ## Regenerate deepcopy code, CRDs and RBAC
	$(CONTROLLER_GEN) object paths="./api/v1alpha1/..."

manifests: ## Regenerate CRDs (config/crd/bases) and RBAC (config/rbac) from markers
	$(CONTROLLER_GEN) crd rbac:roleName=manager-role \
		paths="./api/v1alpha1/..." paths="./internal/controller/..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

install: manifests ## Apply CRDs to the current kube-context
	kubectl apply -f config/crd/bases/

run: ## Run the operator locally against the current kube-context (embedded knowledge base)
	go run ./cmd/operator --ui-path=$(UI_DIR)/dist

docker-build: ## Build the operator image (UI + binary) as $(IMG)
	docker build -t $(IMG) .

deploy: ## Install CRDs, RBAC and the operator into the current kube-context
	kubectl apply -k config/default

undeploy: ## Remove everything deploy created (including the CRDs and their objects)
	kubectl delete -k config/default --ignore-not-found

e2e: ## Smoke test in a throwaway kind cluster (needs docker, kind, kubectl, curl)
	bash hack/e2e.sh

clean: ## Remove build output
	rm -rf bin
