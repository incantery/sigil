# Mako Makefile
#
# Common targets — run `make` (or `make help`) to see them.

BIN_NAME := mako
CMD_PATH := ./core/cmd/mako
PKG      := github.com/incantery/mako/core/cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.1-dev)
LDFLAGS  := -X $(PKG).Version=$(VERSION)

# Honor $GOBIN, fall back to $GOPATH/bin so the install target's message is
# accurate on standard installs.
INSTALL_DIR := $(shell go env GOBIN)
ifeq ($(INSTALL_DIR),)
INSTALL_DIR := $(shell go env GOPATH)/bin
endif

TS_DIR    := editor/tree-sitter-sigil
# Prefer a globally installed tree-sitter CLI; fall back to npx with a
# pinned version so generated parser.c stays stable across machines.
TS_CLI    := $(shell command -v tree-sitter 2>/dev/null)
ifeq ($(TS_CLI),)
TS_CLI    := npx --yes tree-sitter-cli@0.25
endif

# Sigil Studio flagship: the example app whose e2e suite drives a real
# browser against a running server. STUDIO_SLOWMO/STUDIO_HOLD are
# overridable so `make studio-watch STUDIO_SLOWMO=1200ms` slows the
# playback further for a demo.
STUDIO_PKG    := ./examples/studio
STUDIO_SRC    := examples/studio/studio.mako
STUDIO_PORT   ?= 9090
STUDIO_SLOWMO ?= 700ms
STUDIO_HOLD   ?= 2s
STUDIO_PID    := /tmp/sigil-studio.pid
STUDIO_LOG    := /tmp/sigil-studio.log

DOCS_PKG      := ./examples/docs
DOCS_SRC      := examples/docs/docs.mako
DOCS_PORT     ?= 8088
DOCS_PID      := /tmp/sigil-docs.pid
DOCS_LOG      := /tmp/sigil-docs.log

# Streaming chat demo: proves the `stream` op + `<-` arrow-into operator
# (server-push tokens via fetch + ReadableStream).
CHAT_PKG    := ./examples/chat
CHAT_SRC    := examples/chat/chat.mako
CHAT_PORT   ?= 8090
CHAT_SLOWMO ?= 0
CHAT_HOLD   ?= 3s
CHAT_PID    := /tmp/sigil-chat.pid
CHAT_LOG    := /tmp/sigil-chat.log

# Automation gauntlet: a conformance suite of foreign (Sigil-free)
# reference pages, each one a browser-automation hazard a Sigil scenario
# must drive green. Proves the test suite is framework-agnostic.
GAUNTLET_PKG  := ./gauntlet/server
GAUNTLET_PORT ?= 7373
GAUNTLET_PID  := /tmp/sigil-gauntlet.pid
GAUNTLET_LOG  := /tmp/sigil-gauntlet.log
SUITE_TRACE   := /tmp/sigil-suite-traces.json
OTLP_ENDPOINT ?= http://localhost:4318
GRAFANA_URL   ?= http://localhost:3000

.PHONY: build install run test vet fmt tidy clean help tree-sitter tree-sitter-test tree-sitter-verify nvim-parser nvim-install vscode-ext studio-watch studio-run studio-stop docs-run docs-stop docs-test chat-watch chat-run chat-stop gauntlet-run gauntlet-stop gauntlet-test gauntlet-suite observability-up observability-down grafana-dashboard
.DEFAULT_GOAL := help

build: ## Build the mako binary into bin/
	@mkdir -p bin
	@go build -ldflags "$(LDFLAGS)" -o bin/$(BIN_NAME) $(CMD_PATH)
	@echo "→ bin/$(BIN_NAME) ($(VERSION))"

install: ## Install mako to $GOBIN (or $GOPATH/bin)
	@go install -ldflags "$(LDFLAGS)" $(CMD_PATH)
	@echo "→ $(INSTALL_DIR)/$(BIN_NAME) ($(VERSION))"

run: ## Run mako without installing
	@go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

test: ## Run all Go tests
	@go test ./...

gen: ## Run sigil gen (sigil.gen.yaml) + refresh the conformance fixture [legacy kernel]
	@go run ./cmd/sigil gen
	@go test ./pkg/codegen/gogen/conformance -run UpToDate -update

vet: ## Run go vet
	@go vet ./...

fmt: ## gofmt the codebase
	@gofmt -s -w .

tidy: ## go mod tidy
	@go mod tidy

clean: ## Remove the bin/ directory
	@rm -rf bin/

studio-run: build ## Start the Studio server in the background (browse http://localhost:PORT)
	@go build -ldflags "$(LDFLAGS)" -o bin/studio $(STUDIO_PKG)
	@# Free the port first so a re-run replaces any stale server cleanly.
	@-lsof -ti tcp:$(STUDIO_PORT) | xargs kill -9 2>/dev/null || true
	@nohup bin/studio --addr :$(STUDIO_PORT) > $(STUDIO_LOG) 2>&1 & echo $$! > $(STUDIO_PID)
	@sleep 1
	@echo "→ Studio running at http://localhost:$(STUDIO_PORT) (pid $$(cat $(STUDIO_PID)), logs: $(STUDIO_LOG))"
	@echo "  edit $(STUDIO_SRC) and refresh — the server re-reads it per request."
	@echo "  stop with: make studio-stop"

studio-stop: ## Stop the background Studio server started by studio-run
	@if [ -f $(STUDIO_PID) ]; then \
		PID=$$(cat $(STUDIO_PID)); \
		if kill $$PID 2>/dev/null; then echo "→ stopped Studio (pid $$PID)"; else echo "→ no live process for pid $$PID"; fi; \
		rm -f $(STUDIO_PID); \
	else \
		echo "→ no pidfile ($(STUDIO_PID)); freeing port :$(STUDIO_PORT) directly"; \
	fi
	@-lsof -ti tcp:$(STUDIO_PORT) | xargs kill -9 2>/dev/null || true

docs-run: build ## Start the documentation site in the background (browse http://localhost:PORT)
	@go build -ldflags "$(LDFLAGS)" -o bin/docs $(DOCS_PKG)
	@# Free the port first so a re-run replaces any stale server cleanly.
	@-lsof -ti tcp:$(DOCS_PORT) | xargs kill -9 2>/dev/null || true
	@nohup bin/docs --addr :$(DOCS_PORT) > $(DOCS_LOG) 2>&1 & echo $$! > $(DOCS_PID)
	@sleep 1
	@echo "→ Docs running at http://localhost:$(DOCS_PORT) (pid $$(cat $(DOCS_PID)), logs: $(DOCS_LOG))"
	@echo "  edit $(DOCS_SRC) and refresh — the server re-reads it per request."
	@echo "  stop with: make docs-stop"

docs-stop: ## Stop the background docs site started by docs-run
	@if [ -f $(DOCS_PID) ]; then \
		PID=$$(cat $(DOCS_PID)); \
		if kill $$PID 2>/dev/null; then echo "→ stopped Docs (pid $$PID)"; else echo "→ no live process for pid $$PID"; fi; \
		rm -f $(DOCS_PID); \
	else \
		echo "→ no pidfile ($(DOCS_PID)); freeing port :$(DOCS_PORT) directly"; \
	fi
	@-lsof -ti tcp:$(DOCS_PORT) | xargs kill -9 2>/dev/null || true

docs-test: build ## Run the documentation site's e2e scenarios
	@bin/sigil test $(DOCS_SRC)

studio-watch: build ## Launch Studio + run its e2e suite headed (watch the app live)
	@go build -ldflags "$(LDFLAGS)" -o bin/studio $(STUDIO_PKG)
	@# Free the port first — a lingering server silently serves stale JS.
	@-lsof -ti tcp:$(STUDIO_PORT) | xargs kill -9 2>/dev/null || true
	@echo "→ starting Studio on http://localhost:$(STUDIO_PORT) (logs: /tmp/sigil-studio-watch.log)"
	@bin/studio --addr :$(STUDIO_PORT) > /tmp/sigil-studio-watch.log 2>&1 & \
	SRV_PID=$$!; \
	trap 'echo "→ stopping Studio (pid '$$SRV_PID')"; kill $$SRV_PID 2>/dev/null' EXIT INT TERM; \
	printf "→ waiting for server"; \
	for i in $$(seq 1 50); do \
		if curl -fs -o /dev/null http://localhost:$(STUDIO_PORT)/; then echo " ready"; break; fi; \
		printf "."; sleep 0.2; \
		if [ $$i -eq 50 ]; then echo; echo "server did not come up — see /tmp/sigil-studio-watch.log"; exit 1; fi; \
	done; \
	echo "→ running headed e2e (slowmo=$(STUDIO_SLOWMO), hold=$(STUDIO_HOLD)) — watch the browser window"; \
	./bin/$(BIN_NAME) test $(STUDIO_SRC) --headed --slowmo $(STUDIO_SLOWMO) --hold $(STUDIO_HOLD)

chat-run: build ## Start the streaming chat demo in the background (browse http://localhost:PORT)
	@go build -ldflags "$(LDFLAGS)" -o bin/chat $(CHAT_PKG)
	@-lsof -ti tcp:$(CHAT_PORT) | xargs kill -9 2>/dev/null || true
	@nohup bin/chat --addr :$(CHAT_PORT) > $(CHAT_LOG) 2>&1 & echo $$! > $(CHAT_PID)
	@sleep 1
	@echo "→ Chat running at http://localhost:$(CHAT_PORT) (pid $$(cat $(CHAT_PID)), logs: $(CHAT_LOG))"
	@echo "  type a message and watch the reply stream in. stop with: make chat-stop"

chat-stop: ## Stop the background chat demo started by chat-run
	@if [ -f $(CHAT_PID) ]; then \
		PID=$$(cat $(CHAT_PID)); \
		if kill $$PID 2>/dev/null; then echo "→ stopped Chat (pid $$PID)"; else echo "→ no live process for pid $$PID"; fi; \
		rm -f $(CHAT_PID); \
	else \
		echo "→ no pidfile ($(CHAT_PID)); freeing port :$(CHAT_PORT) directly"; \
	fi
	@-lsof -ti tcp:$(CHAT_PORT) | xargs kill -9 2>/dev/null || true

chat-watch: build ## Launch the chat demo + run its e2e headed (watch tokens stream)
	@go build -ldflags "$(LDFLAGS)" -o bin/chat $(CHAT_PKG)
	@-lsof -ti tcp:$(CHAT_PORT) | xargs kill -9 2>/dev/null || true
	@echo "→ starting Chat on http://localhost:$(CHAT_PORT) (logs: /tmp/sigil-chat-watch.log)"
	@bin/chat --addr :$(CHAT_PORT) > /tmp/sigil-chat-watch.log 2>&1 & \
	SRV_PID=$$!; \
	trap 'echo "→ stopping Chat (pid '$$SRV_PID')"; kill $$SRV_PID 2>/dev/null' EXIT INT TERM; \
	printf "→ waiting for server"; \
	for i in $$(seq 1 50); do \
		if curl -fs -o /dev/null http://localhost:$(CHAT_PORT)/; then echo " ready"; break; fi; \
		printf "."; sleep 0.2; \
		if [ $$i -eq 50 ]; then echo; echo "server did not come up — see /tmp/sigil-chat-watch.log"; exit 1; fi; \
	done; \
	echo "→ running headed e2e (hold=$(CHAT_HOLD)) — watch the reply stream in"; \
	./bin/$(BIN_NAME) test $(CHAT_SRC) --headed --slowmo $(CHAT_SLOWMO) --hold $(CHAT_HOLD)

gauntlet-run: ## Start the gauntlet page server in the background (browse http://localhost:PORT)
	@go build -ldflags "$(LDFLAGS)" -o bin/gauntlet $(GAUNTLET_PKG)
	@-lsof -ti tcp:$(GAUNTLET_PORT) | xargs kill -9 2>/dev/null || true
	@nohup bin/gauntlet -addr :$(GAUNTLET_PORT) > $(GAUNTLET_LOG) 2>&1 & echo $$! > $(GAUNTLET_PID)
	@sleep 1
	@echo "→ Gauntlet pages at http://localhost:$(GAUNTLET_PORT) (pid $$(cat $(GAUNTLET_PID)), logs: $(GAUNTLET_LOG))"
	@echo "  drive a challenge: sigil test gauntlet/<challenge>.mako. stop with: make gauntlet-stop"

gauntlet-stop: ## Stop the background gauntlet server started by gauntlet-run
	@if [ -f $(GAUNTLET_PID) ]; then \
		PID=$$(cat $(GAUNTLET_PID)); \
		if kill $$PID 2>/dev/null; then echo "→ stopped gauntlet (pid $$PID)"; else echo "→ no live process for pid $$PID"; fi; \
		rm -f $(GAUNTLET_PID); \
	else \
		echo "→ no pidfile ($(GAUNTLET_PID)); freeing port :$(GAUNTLET_PORT) directly"; \
	fi
	@-lsof -ti tcp:$(GAUNTLET_PORT) | xargs kill -9 2>/dev/null || true

gauntlet-test: build ## Boot the gauntlet server, drive every challenge, tear down
	@go build -ldflags "$(LDFLAGS)" -o bin/gauntlet $(GAUNTLET_PKG)
	@-lsof -ti tcp:$(GAUNTLET_PORT) | xargs kill -9 2>/dev/null || true
	@echo "→ starting gauntlet on http://localhost:$(GAUNTLET_PORT) (logs: $(GAUNTLET_LOG))"
	@bin/gauntlet -addr :$(GAUNTLET_PORT) > $(GAUNTLET_LOG) 2>&1 & \
	SRV_PID=$$!; \
	trap 'kill $$SRV_PID 2>/dev/null' EXIT INT TERM; \
	printf "→ waiting for server"; \
	for i in $$(seq 1 50); do \
		if curl -fs -o /dev/null http://localhost:$(GAUNTLET_PORT)/; then echo " ready"; break; fi; \
		printf "."; sleep 0.2; \
		if [ $$i -eq 50 ]; then echo; echo "server did not come up — see $(GAUNTLET_LOG)"; exit 1; fi; \
	done; \
	echo "→ driving every challenge (sigil test compiles the whole package)"; \
	./bin/$(BIN_NAME) test $$(ls gauntlet/*.mako | head -1)

observability-up: ## Bring up the LGTM stack (Tilt + k8s; works with remote docker)
	@echo "→ tilt up — wait for the 'lgtm' resource to go green, then 'make gauntlet-suite'"
	@echo "  (needs a dev k8s context: docker-desktop / orbstack / homelab / kind / minikube)"
	tilt up

observability-down: ## Tear down the LGTM stack
	tilt down

gauntlet-suite: build ## Run the whole gauntlet, push traces to Tempo (if up), print a Grafana review URL
	@go build -ldflags "$(LDFLAGS)" -o bin/gauntlet $(GAUNTLET_PKG)
	@-lsof -ti tcp:$(GAUNTLET_PORT) | xargs kill -9 2>/dev/null || true
	@echo "→ starting gauntlet pages on http://localhost:$(GAUNTLET_PORT) (logs: $(GAUNTLET_LOG))"
	@bin/gauntlet -addr :$(GAUNTLET_PORT) > $(GAUNTLET_LOG) 2>&1 & \
	SRV=$$!; \
	trap 'kill $$SRV 2>/dev/null' EXIT INT TERM; \
	printf "→ waiting for server"; \
	for i in $$(seq 1 50); do \
		if curl -fs -o /dev/null http://localhost:$(GAUNTLET_PORT)/; then echo " ready"; break; fi; \
		printf "."; sleep 0.2; \
		if [ $$i -eq 50 ]; then echo; echo "server did not come up — see $(GAUNTLET_LOG)"; exit 1; fi; \
	done; \
	OTLP_ARGS=""; \
	if nc -z localhost 4318 2>/dev/null; then \
		OTLP_ARGS="--otlp $(OTLP_ENDPOINT)"; \
		echo "→ LGTM reachable — pushing traces to $(OTLP_ENDPOINT)"; \
	else \
		echo "→ LGTM not reachable on :4318 — run 'make observability-up' (tilt up) first to review in Grafana"; \
	fi; \
	./bin/$(BIN_NAME) test $$(ls gauntlet/*.mako | head -1) --trace $(SUITE_TRACE) $$OTLP_ARGS; \
	RC=$$?; \
	echo; \
	echo "→ trace artifact: $(SUITE_TRACE)"; \
	if [ -n "$$OTLP_ARGS" ]; then \
		echo "→ review the run in Grafana:"; \
		echo "   $$(python3 scripts/grafana_dashboard.py $(GRAFANA_URL))"; \
	fi; \
	exit $$RC

grafana-dashboard: ## Install/update the Sigil scenarios dashboard in a running Grafana
	@python3 scripts/grafana_dashboard.py $(GRAFANA_URL)

tree-sitter: ## Regenerate the tree-sitter parser (+ rebuild the nvim .so so it never goes stale)
	@cd $(TS_DIR) && $(TS_CLI) generate
	@echo "→ $(TS_DIR)/src/parser.c"
	@# Rebuild the loadable parser too: nvim (and the queries it loads) read
	@# $(TS_DIR)/sigil.so directly, so a grammar change that doesn't rebuild
	@# it leaves nvim on a stale parser and the highlight queries error on
	@# the new nodes. Best-effort: a missing C compiler shouldn't fail regen.
	@$(MAKE) --no-print-directory nvim-parser 2>/dev/null \
		|| echo "  (nvim sigil.so not rebuilt — cc unavailable; run 'make nvim-parser' by hand)"

tree-sitter-test: ## Run tree-sitter corpus tests + query validation
	@cd $(TS_DIR) && $(TS_CLI) test

tree-sitter-verify: ## Drift guard: parse every example app; fail on ERROR nodes
	@cd $(TS_DIR) && find ../../examples -name '*.mako' -print0 | \
		xargs -0 $(TS_CLI) parse --quiet --stat > /tmp/sigil-ts-verify.txt; \
	tr -d '\r' < /tmp/sigil-ts-verify.txt | tail -1; \
	grep -q 'failed parses: 0;' /tmp/sigil-ts-verify.txt \
		|| { echo "✗ grammar drift: some examples no longer parse — see /tmp/sigil-ts-verify.txt"; exit 1; }

nvim-parser: ## Build the tree-sitter parser as a loadable .so for Neovim
	@cd $(TS_DIR) && cc -shared -fPIC -Wall -Os -I src \
		src/parser.c src/scanner.c -o sigil.so
	@echo "→ $(TS_DIR)/sigil.so (have nvim load it via vim.treesitter.language.add)"

nvim-install: nvim-parser ## Install parser + queries + ftdetect into ~/.local/share/nvim/site
	@SITE=$${XDG_DATA_HOME:-$$HOME/.local/share}/nvim/site; \
	mkdir -p $$SITE/parser $$SITE/queries/sigil $$SITE/ftdetect; \
	cp $(TS_DIR)/sigil.so $$SITE/parser/; \
	cp $(TS_DIR)/queries/*.scm $$SITE/queries/sigil/; \
	cp editor/nvim/ftdetect/sigil.lua $$SITE/ftdetect/; \
	echo "→ installed parser, queries, ftdetect under $$SITE"; \
	echo "  see editor/README.md for the lspconfig snippet (sigil lsp)"

vscode-ext: ## Package the VS Code extension (.vsix) — needs npm
	@cd editor/vscode-sigil && npm install --no-audit --no-fund && npx --yes @vscode/vsce package --allow-missing-repository
	@echo "→ editor/vscode-sigil/*.vsix (install: code --install-extension <file>)"

help: ## Show this help
	@printf "\n\033[1mSigil — Go-native UI compiler\033[0m\n\nTargets:\n"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf "\nBuild metadata:\n  VERSION = %s\n  INSTALL_DIR = %s\n\n" "$(VERSION)" "$(INSTALL_DIR)"
