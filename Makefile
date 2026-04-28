BINARY_OPERATOR := bin/operator
BINARY_CLI      := bin/vilt

.PHONY: build test generate lint

build:
	go build -o $(BINARY_OPERATOR) ./cmd/operator/...
	go build -o $(BINARY_CLI) ./cmd/cli/...

test:
	go test ./...

generate:
	go generate ./...

lint:
	golangci-lint run ./...
