# Thor Go port — developer Makefile.
#
# All targets assume CWD = golang/. Out-of-tree invocations should `cd golang/`
# first or use `make -C golang <target>`.

MODULE      := thor-golang
BIN_DIR     := bin
DIST_DIR    := dist
COVER_FILE  := coverage.out

THOR_INSTALL_DIR ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.BuildDate=$(DATE)

GO       ?= go
GOFLAGS  ?= -trimpath
GO_BUILD := CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)'

.PHONY: all build install-local uninstall-local test test-cover lint vet fmt tidy \
	sync-prompts verify-prompts release-snapshot cross-compile npm-prepare clean help

all: build

build: ## Build thor and thor-mcp into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD) -o $(BIN_DIR)/thor ./cmd/thor
	$(GO_BUILD) -o $(BIN_DIR)/thor-mcp ./cmd/thor-mcp
	@echo "Built: $(BIN_DIR)/thor, $(BIN_DIR)/thor-mcp"

install-local: build ## Copy binaries to $(THOR_INSTALL_DIR)
	@mkdir -p $(THOR_INSTALL_DIR)
	install -m 0755 $(BIN_DIR)/thor      $(THOR_INSTALL_DIR)/thor
	install -m 0755 $(BIN_DIR)/thor-mcp  $(THOR_INSTALL_DIR)/thor-mcp
	@echo
	@echo "Installed to $(THOR_INSTALL_DIR)"
	@case ":$$PATH:" in \
		*":$(THOR_INSTALL_DIR):"*) echo "($(THOR_INSTALL_DIR) is already on PATH)" ;; \
		*) echo "Add to your shell rc: export PATH=\"$(THOR_INSTALL_DIR):\$$PATH\"" ;; \
	esac

uninstall-local: ## Remove binaries from $(THOR_INSTALL_DIR)
	rm -f $(THOR_INSTALL_DIR)/thor $(THOR_INSTALL_DIR)/thor-mcp
	@echo "Removed from $(THOR_INSTALL_DIR)"

test: ## Run unit tests
	$(GO) test ./...

test-cover: ## Run unit tests with coverage report
	$(GO) test -coverprofile=$(COVER_FILE) ./...
	$(GO) tool cover -func=$(COVER_FILE) | tail -1

lint: ## Run golangci-lint
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed. See https://golangci-lint.run/welcome/install/"; \
		exit 1; \
	}
	golangci-lint run ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format with gofmt + goimports
	gofmt -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || true

tidy: ## go mod tidy
	$(GO) mod tidy

sync-prompts: ## Copy prompts from ../server/thor/tools/ into ./prompts/
	./scripts/sync-prompts.sh

verify-prompts: ## Fail if prompts/ differs from ../server/thor/tools/
	@./scripts/verify-prompts.sh

release-snapshot: ## Local GoReleaser snapshot (no publish)
	@command -v goreleaser >/dev/null 2>&1 || { \
		echo "goreleaser not installed. See https://goreleaser.com/install/"; \
		exit 1; \
	}
	goreleaser release --snapshot --clean

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(DIST_DIR) $(COVER_FILE)

cross-compile: ## Cross-compile for all platforms into dist/
	@mkdir -p $(DIST_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO_BUILD) -o $(DIST_DIR)/thor-mcp-darwin-arm64  ./cmd/thor-mcp
	GOOS=darwin  GOARCH=amd64 $(GO_BUILD) -o $(DIST_DIR)/thor-mcp-darwin-x64    ./cmd/thor-mcp
	GOOS=linux   GOARCH=amd64 $(GO_BUILD) -o $(DIST_DIR)/thor-mcp-linux-x64     ./cmd/thor-mcp
	GOOS=linux   GOARCH=arm64 $(GO_BUILD) -o $(DIST_DIR)/thor-mcp-linux-arm64   ./cmd/thor-mcp
	GOOS=windows GOARCH=amd64 $(GO_BUILD) -o $(DIST_DIR)/thor-mcp-win32-x64.exe ./cmd/thor-mcp
	@echo "Cross-compiled to $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

npm-prepare: cross-compile ## Build all platforms and stage into npm packages
	@echo "Staging binaries into npm packages..."
	@cp $(DIST_DIR)/thor-mcp-darwin-arm64  npm/darwin-arm64/bin/thor-mcp
	@cp $(DIST_DIR)/thor-mcp-darwin-x64    npm/darwin-x64/bin/thor-mcp
	@cp $(DIST_DIR)/thor-mcp-linux-x64     npm/linux-x64/bin/thor-mcp
	@cp $(DIST_DIR)/thor-mcp-linux-arm64   npm/linux-arm64/bin/thor-mcp
	@cp $(DIST_DIR)/thor-mcp-win32-x64.exe npm/win32-x64/bin/thor-mcp.exe
	@chmod +x npm/darwin-arm64/bin/thor-mcp npm/darwin-x64/bin/thor-mcp npm/linux-x64/bin/thor-mcp npm/linux-arm64/bin/thor-mcp
	@echo "Done. Publish with: make npm-publish"

help: ## Show available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'
