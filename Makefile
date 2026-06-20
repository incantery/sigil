# Sigil Makefile
#
# Common targets — run `make` (or `make help`) to see them.

BIN_NAME := sigil
CMD_PATH := ./core/cmd/sigil
PKG      := github.com/incantery/sigil/core/cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.1-dev)
LDFLAGS  := -X $(PKG).Version=$(VERSION)

# Honor $GOBIN, fall back to $GOPATH/bin so the install target's message is
# accurate on standard installs.
INSTALL_DIR := $(shell go env GOBIN)
ifeq ($(INSTALL_DIR),)
INSTALL_DIR := $(shell go env GOPATH)/bin
endif

.PHONY: build install run test vet fmt tidy clean help
.DEFAULT_GOAL := help

build: ## Build the sigil binary into bin/
	@mkdir -p bin
	@go build -ldflags "$(LDFLAGS)" -o bin/$(BIN_NAME) $(CMD_PATH)
	@echo "→ bin/$(BIN_NAME) ($(VERSION))"

install: ## Install sigil to $GOBIN (or $GOPATH/bin)
	@go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	@echo "→ $(INSTALL_DIR)/$(BIN_NAME) ($(VERSION))"

run: ## Run sigil without installing (e.g. make run -- serve core/examples/counter/counter.sigil)
	@go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

test: ## Run all Go tests
	@go test ./...

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
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf "\nBuild metadata:\n  VERSION = %s\n  INSTALL_DIR = %s\n\n" "$(VERSION)" "$(INSTALL_DIR)"
