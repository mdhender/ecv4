OAPI_CODEGEN ?= oapi-codegen
SPEC := api/openapi.yaml
CODEGEN_CFG := api/oapi-codegen.yaml
GENERATED := internal/api/openapi.gen.go
BINARY := bin/game-server

.PHONY: help
help:
	@printf '%s\n' 'Targets:'
	@printf '%s\n' '  make install-tools   Install oapi-codegen with go install'
	@printf '%s\n' '  make generate        Generate Go DTOs and server stubs from api/openapi.yaml'
	@printf '%s\n' '  make test            Run go test ./...'
	@printf '%s\n' '  make build           Build cmd/game-server'
	@printf '%s\n' '  make run             Run the skeleton server'
	@printf '%s\n' '  make clean           Remove local build products'

.PHONY: install-tools
install-tools:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

.PHONY: generate
generate:
	$(OAPI_CODEGEN) --config $(CODEGEN_CFG) $(SPEC)

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/game-server

.PHONY: run
run:
	go run ./cmd/game-server

.PHONY: clean
clean:
	rm -rf bin
