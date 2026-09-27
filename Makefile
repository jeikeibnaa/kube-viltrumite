BINARY_OPERATOR := bin/operator
KNOWLEDGE_DIR   := knowledge/tools
UI_DIR          := ui

# controller-gen runs pinned via `go run pkg@version` so it works the same on
# Windows, Linux and CI without a GOPATH install and without touching go.mod.
CONTROLLER_TOOLS_VERSION := v0.21.0
CONTROLLER_GEN           := go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

# The `vilt` kubectl plugin (cmd/cli) lands in v0.9.0; until then build is operator-only.

.PHONY: help build ui test vet lint verify generate manifests install run clean

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-10s %s\n", $$1, $$2}'

build: ## Build the operator binary into bin/
	go build -o $(BINARY_OPERATOR) ./cmd/operator

ui: ## Install UI deps from the lockfile and build ui/dist
	cd $(UI_DIR) && npm ci && npm run build

test: ## Run unit tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (must be installed; CI provides it)
	golangci-lint run ./...

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

run: ## Run the operator locally against the current kube-context
	go run ./cmd/operator --knowledge-base-path=$(KNOWLEDGE_DIR) --ui-path=$(UI_DIR)/dist

clean: ## Remove build output
	rm -rf bin
