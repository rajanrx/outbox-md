# outbox-md — turnkey ops layer.
#
# One command each to run the server, start a runner, tail its logs, and check
# status. Wraps the published image (docker-compose.yml) and the reference
# runners (examples/runner/{go,node,python}) — no runner code is duplicated here.
#
# Run `make` (or `make help`) for the menu. Every variable below is overridable
# on the command line, e.g. `make runner RUNNER_LANG=go RUNNER_PORT=9000`.
#
# NOTE: `lsof` (used by `runner`/`status`) is macOS/Linux only. On Windows use
# WSL, or start the runner directly (see deploy/README.md).

# --- Overridable configuration -------------------------------------------------
RUNNER_PORT           ?= 8787
RUNNER_LANG           ?= python
OUTBOX_WEBHOOK_SECRET ?= change-me
# Listen address handed to the runner; defaults to all interfaces on RUNNER_PORT.
RUNNER_ADDR           ?= :$(RUNNER_PORT)
# cli-mode agent command template. Empty ⇒ the runner uses its built-in default
# (`claude -p {prompt} --allowedTools mcp__outbox-md__*`). Override to wrap the
# agent for a corporate proxy / custom auth — see deploy/README.md.
RUNNER_AGENT_CMD      ?=

# Server URL printed after `up`.
SERVER_URL := http://localhost:8181
# Where the detached runner writes its log.
RUNNER_LOG := runner.log

# Env shared by every runner language. RUNNER_AGENT_CMD is exported empty when
# unset, and the runner falls back to its own default in that case.
RUNNER_ENV = OUTBOX_WEBHOOK_SECRET='$(OUTBOX_WEBHOOK_SECRET)' \
             RUNNER_ADDR='$(RUNNER_ADDR)' \
             RUNNER_AGENT_CMD='$(RUNNER_AGENT_CMD)'

.DEFAULT_GOAL := help
.PHONY: help up down pull update runner logs status

help: ## Show this help menu
	@echo 'outbox-md — turnkey ops. Usage: make <target> [VAR=value]'
	@echo ''
	@echo 'Targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
	@echo ''
	@echo 'Variables (override on the command line):'
	@echo '  RUNNER_PORT=$(RUNNER_PORT)  RUNNER_LANG=$(RUNNER_LANG)  OUTBOX_WEBHOOK_SECRET=$(OUTBOX_WEBHOOK_SECRET)'

up: ## Start the outbox-md server (detached; idempotent)
	docker compose up -d
	@echo "Server up → $(SERVER_URL)"

down: ## Stop the outbox-md server
	docker compose down

pull: ## Pull the latest server image and recreate
	docker compose pull
	docker compose up -d
	@echo "Server updated → $(SERVER_URL)"

update: pull ## Alias for pull (fetch latest image + recreate)

runner: ## Start the webhook runner detached (RUNNER_LANG=python|go|node)
	@pids=$$(lsof -ti tcp:$(RUNNER_PORT) 2>/dev/null); \
	if [ -n "$$pids" ]; then \
		echo "Freeing port $(RUNNER_PORT) (killing $$pids)"; \
		kill $$pids 2>/dev/null || true; \
		sleep 1; \
	fi; \
	case '$(RUNNER_LANG)' in \
		python) cmd='python3 examples/runner/python/main.py' ;; \
		node)   cmd='node examples/runner/node/index.js' ;; \
		go)     cmd='go run .'; dir='examples/runner/go' ;; \
		*) echo "Unknown RUNNER_LANG=$(RUNNER_LANG) (want python|go|node)"; exit 2 ;; \
	esac; \
	echo "Starting $(RUNNER_LANG) runner on $(RUNNER_ADDR) → $(RUNNER_LOG)"; \
	if [ "$(RUNNER_LANG)" = "go" ]; then \
		( cd "$$dir" && $(RUNNER_ENV) nohup $$cmd ) > $(RUNNER_LOG) 2>&1 & \
	else \
		$(RUNNER_ENV) nohup $$cmd > $(RUNNER_LOG) 2>&1 & \
	fi; \
	echo "  pid $$! — tail with: make logs"; \
	ok=0; \
	for i in 1 2 3 4 5 6 7 8; do \
		sleep 1; \
		if lsof -ti tcp:$(RUNNER_PORT) >/dev/null 2>&1; then ok=1; break; fi; \
	done; \
	if [ "$$ok" = "1" ]; then \
		echo "Runner listening on port $(RUNNER_PORT)."; \
	else \
		echo "Runner did NOT bind port $(RUNNER_PORT) — last log lines:"; \
		tail -n 15 $(RUNNER_LOG) 2>/dev/null || true; \
		exit 1; \
	fi

logs: ## Tail the runner log (Ctrl-C to stop)
	tail -f $(RUNNER_LOG)

status: ## Show server containers + whether the runner port is listening
	@echo '== server (docker compose) =='
	@docker compose ps 2>/dev/null || echo '  (docker unavailable or no compose project)'
	@echo '== runner (port $(RUNNER_PORT)) =='
	@if lsof -ti tcp:$(RUNNER_PORT) >/dev/null 2>&1; then \
		echo "  listening (pid $$(lsof -ti tcp:$(RUNNER_PORT) 2>/dev/null | tr '\n' ' '))"; \
	else \
		echo '  not listening'; \
	fi
