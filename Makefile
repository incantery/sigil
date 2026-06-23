# Sigil Makefile
#
# Common targets — run `make` (or `make help`) to see them.

BIN_NAME := sigil
CMD_PATH := ./cmd/sigil
PKG      := github.com/incantery/sigil/internal/cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.1-dev)
LDFLAGS  := -X $(PKG).Version=$(VERSION)

# Honor $GOBIN, fall back to $GOPATH/bin so the install target's message is
# accurate on standard installs.
INSTALL_DIR := $(shell go env GOBIN)
ifeq ($(INSTALL_DIR),)
INSTALL_DIR := $(shell go env GOPATH)/bin
endif

.PHONY: build install run test test-sigil test-browser vet fmt tidy clean help \
        tree-sitter tree-sitter-test tree-sitter-verify nvim-install vscode-ext

TS_DIR := editor/tree-sitter-sigil
TS_CLI := npx --yes tree-sitter-cli@0.25
.DEFAULT_GOAL := help

build: ## Build the sigil binary into bin/
	@mkdir -p bin
	@go build -ldflags "$(LDFLAGS)" -o bin/$(BIN_NAME) $(CMD_PATH)
	@echo "→ bin/$(BIN_NAME) ($(VERSION))"

install: ## Install sigil to $GOBIN (or $GOPATH/bin)
	@go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	@echo "→ $(INSTALL_DIR)/$(BIN_NAME) ($(VERSION))"

run: ## Run sigil without installing (e.g. make run -- serve examples/counter/counter.sigil)
	@go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

test: ## Run all Go tests
	@go test ./...

test-sigil: build ## Run *_test.sigil suite via the sigil test runner
	./bin/sigil test tests --root .

test-browser: build ## Run browser *_test.sigil (requires a served app + Chrome)
	./bin/sigil test testdata/browser --root .

vet: ## Run go vet
	@go vet ./...

fmt: ## gofmt the codebase
	@gofmt -s -w .

tidy: ## go mod tidy
	@go mod tidy

clean: ## Remove the bin/ directory
	@rm -rf bin/

help: ## Show this help
	@printf "\n\033[1mSigil — a typed reactive UI language\033[0m\n\nTargets:\n"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf "\nBuild metadata:\n  VERSION = %s\n  INSTALL_DIR = %s\n\n" "$(VERSION)" "$(INSTALL_DIR)"

tree-sitter: ## Regenerate the tree-sitter parser from grammar.js
	@cd $(TS_DIR) && $(TS_CLI) generate
	@echo "→ $(TS_DIR)/src/parser.c"

tree-sitter-test: ## Run the tree-sitter grammar corpus tests
	@cd $(TS_DIR) && $(TS_CLI) test

tree-sitter-verify: ## Parse every std/ + examples/ .sigil; fail on ERROR nodes
	@cd $(TS_DIR) && $(TS_CLI) generate >/dev/null
	@found=0; for f in $$(cd $(TS_DIR) && find ../../std ../../examples -name '*.sigil' 2>/dev/null); do \
		out=$$(cd $(TS_DIR) && $(TS_CLI) parse -q "$$f" 2>&1); \
		if echo "$$out" | grep -q '(ERROR'; then echo "✗ parse error: $$f"; found=1; fi; \
	done; \
	if [ $$found -eq 0 ]; then echo "✓ all .sigil files parse clean"; else exit 1; fi

nvim-install: tree-sitter ## Build the parser .so + install queries + ftdetect into nvim site
	@cd $(TS_DIR) && cc -shared -fPIC -Wall -Os -I src src/parser.c src/scanner.c -o sigil.so
	@SITE=$${XDG_DATA_HOME:-$$HOME/.local/share}/nvim/site; \
	mkdir -p $$SITE/parser $$SITE/queries/sigil $$SITE/ftdetect; \
	cp $(TS_DIR)/sigil.so $$SITE/parser/; \
	cp $(TS_DIR)/queries/*.scm $$SITE/queries/sigil/; \
	cp editor/nvim/ftdetect/sigil.lua $$SITE/ftdetect/; \
	echo "→ installed parser, queries, ftdetect under $$SITE"

vscode-ext: ## Package the VS Code extension (.vsix) — needs npm
	@cd editor/vscode-sigil && npm install --no-audit --no-fund && npx --yes @vscode/vsce package --allow-missing-repository
	@echo "→ editor/vscode-sigil/*.vsix (install: code --install-extension <file>)"
