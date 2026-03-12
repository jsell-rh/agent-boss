NAMESPACE     := jsell-agent-boss
IMAGE_NAME    := boss-coordinator
REGISTRY      := default-route-openshift-image-registry.apps.okd1.timslab
IMAGE_TAG     := latest
IMAGE         := $(REGISTRY)/$(NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build install build-image push-image deploy rollout dev-build dev-start dev-stop dev-restart dev-status

build:
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go build -o boss ./cmd/boss/

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
DEV_BIN    := $(DEV_DATA)/boss
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

# E2E tests (Playwright)
e2e:
	cd e2e && npx playwright test

e2e-ui:
	cd e2e && npx playwright test --headed

e2e-report:
	cd e2e && npx playwright show-report
