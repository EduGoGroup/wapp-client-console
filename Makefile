# Makefile — wapp-client-console
#
# Régimen CI/CD local: validación con gates fijados.
#   - ci-local  agrega los gates que exige el grupo (fmt, vet, lint, test, build).
#   - ci-docker reproduce el toolchain fijado (imagen golang + golangci-lint).

GO_VERSION   := 1.26.5
LINT_VERSION := v2.12.2
GO           := GOWORK=off go

.PHONY: fmt-check vet lint build test ci-local ci-docker run

fmt-check: ## gofmt -l vacío (sin archivos sin formatear)
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Archivos sin gofmt:"; echo "$$unformatted"; exit 1; \
	fi

vet: ## go vet ./...
	$(GO) vet ./...

lint: ## golangci-lint
	GOWORK=off golangci-lint run --timeout=5m

build: ## go build ./...
	$(GO) build ./...

test: ## Tests unitarios con -race
	$(GO) test -race ./...

run: ## Arranca la consola en local
	$(GO) run ./cmd/client-console/main.go

ci-local: fmt-check vet lint test build ## Pre-push: fmt + vet + lint + test + build

ci-docker: ## Simula el CI en Docker
	@docker run --rm \
		-e GOFLAGS=-buildvcs=false \
		-v "$$(go env GOPATH)/pkg/mod:/go/pkg/mod" \
		-v "$(CURDIR)/../..:/workspace" -w /workspace/guardian/wapp-client-console \
		golang:$(GO_VERSION)-bookworm \
		bash -c "set -e; curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b /usr/local/bin $(LINT_VERSION) && make ci-local"
