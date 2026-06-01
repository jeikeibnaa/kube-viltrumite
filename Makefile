BINARY_OPERATOR := bin/operator
BINARY_CLI      := bin/vilt
CONTROLLER_GEN  := $(shell go env GOPATH)/bin/controller-gen

.PHONY: build test generate manifests lint install run

build:
	go build -o $(BINARY_OPERATOR) ./cmd/operator/...
	go build -o $(BINARY_CLI) ./cmd/cli/...

test:
	go test ./...

generate:
	go generate ./...

manifests:
	$(CONTROLLER_GEN) crd paths="./api/v1alpha1/..." output:crd:artifacts:config=config/crd/bases

install: manifests
	kubectl apply -f config/crd/bases/

run:
	go run ./cmd/operator/...

lint:
	golangci-lint run ./...