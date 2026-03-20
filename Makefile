NAMESPACE     := jsell-agent-boss
IMAGE_NAME    := boss-coordinator
REGISTRY      := default-route-openshift-image-registry.apps.okd1.timslab
IMAGE_TAG     := latest
IMAGE         := $(REGISTRY)/$(NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build install build-image push-image deploy rollout dev-build dev-start dev-stop dev-restart dev-status dev-spawn e2e e2e-ui e2e-report e2e-dev e2e-screenshots typecheck install-hooks


typecheck:
	cd frontend && npx vue-tsc -b

install-hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks installed. Pre-commit hook will typecheck frontend on staged .ts/.vue files."

build:
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go build -o odis ./cmd/boss/

install:
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go install ./cmd/boss/

build-image:
	podman build -t $(IMAGE) -f deploy/Dockerfile .

push-image:
	podman push $(IMAGE) --tls-verify=false

deploy:
	oc apply -f deploy/openshift/namespace.yaml
	oc process -f deploy/openshift/postgresql-credentials.yaml | oc apply -f -
	oc process -f deploy/openshift/ambient-credentials.yaml | oc apply -f -
	oc apply -f deploy/openshift/configmap.yaml
	oc apply -f deploy/openshift/postgresql.yaml
	oc apply -f deploy/openshift/deployment.yaml
	oc apply -f deploy/openshift/service.yaml
	oc apply -f deploy/openshift/route.yaml

rollout: build-image push-image
	oc rollout restart deploy/boss-coordinator -n $(NAMESPACE)

# ── Per-worktree dev instance ─────────────────────────────────────────────────
# Each worktree gets its own isolated boss instance: own port, own data, own PID.
# Set DEV_PORT explicitly or let make auto-detect the first free port >= 9000.

DEV_DATA   := ./data-dev
DEV_BIN    := $(DEV_DATA)/odis
DEV_LOG    := $(DEV_DATA)/boss.log
DEV_PID    := $(DEV_DATA)/boss.pid
DEV_PORT_F := $(DEV_DATA)/boss.port

dev-build:
	@mkdir -p $(DEV_DATA)
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go build -o $(DEV_BIN) ./cmd/boss/
	@echo "dev-build: binary ready at $(DEV_BIN)"

dev-start: dev-build
	@mkdir -p $(DEV_DATA)
	@if [ -f $(DEV_PID) ] && kill -0 "$$(cat $(DEV_PID))" 2>/dev/null; then \
		echo "dev instance already running (PID=$$(cat $(DEV_PID)), port=$$(cat $(DEV_PORT_F) 2>/dev/null))"; \
		exit 0; \
	fi
	@if [ -n "$(DEV_PORT)" ]; then \
		PORT=$(DEV_PORT); \
	else \
		PORT=9000; \
		while ss -tlnH 2>/dev/null | awk '{print $$4}' | grep -qE ":$$PORT$$" || \
		      (command -v lsof >/dev/null 2>&1 && lsof -ti:$$PORT >/dev/null 2>&1); do \
			PORT=$$((PORT + 1)); \
		done; \
	fi; \
	echo $$PORT > $(DEV_PORT_F); \
	COORDINATOR_PORT=$$PORT DATA_DIR=$(DEV_DATA) nohup $(DEV_BIN) serve >> $(DEV_LOG) 2>&1 & \
	echo $$! > $(DEV_PID); \
	echo "dev instance started: port=$$PORT PID=$$! log=$(DEV_LOG)"

dev-stop:
	@if [ ! -f $(DEV_PID) ]; then echo "dev instance not running (no PID file)"; exit 0; fi
	@PID=$$(cat $(DEV_PID)); \
	if kill -0 "$$PID" 2>/dev/null; then \
		kill "$$PID" && echo "dev instance stopped (PID=$$PID)"; \
	else \
		echo "dev instance already stopped (stale PID=$$PID)"; \
	fi; \
	rm -f $(DEV_PID)

dev-restart: dev-stop dev-start

dev-status:
	@PORT=$$(cat $(DEV_PORT_F) 2>/dev/null || echo "unknown"); \
	if [ -f $(DEV_PID) ] && kill -0 "$$(cat $(DEV_PID))" 2>/dev/null; then \
		echo "dev instance RUNNING — port=$$PORT PID=$$(cat $(DEV_PID)) url=http://localhost:$$PORT"; \
	else \
		echo "dev instance STOPPED — last port=$$PORT"; \
	fi; \
	if [ -f $(DEV_LOG) ]; then \
		echo "--- last 20 log lines ($(DEV_LOG)) ---"; \
		tail -20 $(DEV_LOG); \
	fi

# ── Dev agent spawner ─────────────────────────────────────────────────────────
# Spawn a tmux agent session pre-wired with both boss-mcp and boss-dev MCP servers.
# Usage: make dev-spawn AGENT=myagent SPACE="My Space" [WORK_DIR=/path/to/dir]
#
# The spawned agent can use:
#   boss-mcp.*   — production coordinator tools (post_status, check_messages, etc.)
#   boss-dev.*   — local dev instance tools (test against your branch's code)
#
# Set BOSS_API_TOKEN in env to forward auth credentials to boss-mcp.

AGENT     ?= dev-agent
SPACE     ?= Agent Boss Dev
WORK_DIR  ?= $(CURDIR)

dev-spawn:
	@bash scripts/spawn-dev-agent.sh "$(AGENT)" "$(SPACE)" "$(WORK_DIR)"

# ── Dev E2E tests ──────────────────────────────────────────────────────────────
# Run Playwright e2e tests against the dev instance instead of production.
# Requires: make dev-start (dev instance must be running)

e2e-dev:
	@PORT=$$(cat $(DEV_PORT_F) 2>/dev/null || echo ""); \
	if [ -z "$$PORT" ]; then \
		echo "e2e-dev: dev instance not started — run 'make dev-start' first" >&2; \
		exit 1; \
	fi; \
	echo "e2e-dev: running Playwright against http://localhost:$$PORT"; \
	cd e2e && BASE_URL="http://localhost:$$PORT" npx playwright test

# E2E tests (Playwright)
e2e:
	cd e2e && npx playwright test

e2e-ui:
	cd e2e && npx playwright test --headed

e2e-report:
	cd e2e && npx playwright show-report

# Run e2e against a running dev instance (no rebuild). DEV_PORT defaults to 9000.
e2e-dev:
	cd e2e && BASE_URL=http://localhost:$${DEV_PORT:-9000} SKIP_BUILD=1 npx playwright test

# Capture screenshots of key UI pages for agent visual inspection.
# Reads BOSS_URL (default http://localhost:8899). Output: e2e/snapshots/*.png
e2e-screenshots:
	cd e2e && BOSS_URL=$${BOSS_URL:-http://localhost:8899} npx tsx scripts/screenshots.ts
