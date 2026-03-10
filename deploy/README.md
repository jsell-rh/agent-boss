# OpenShift Deployment

Deploys jsell-agent-boss to an OKD/OpenShift cluster with PostgreSQL for persistent storage.

## Prerequisites

- `oc` CLI logged into the target cluster
- `podman` installed
- Access to the cluster's internal image registry

## Registry Login

```bash
podman login default-route-openshift-image-registry.apps.okd1.timslab --tls-verify=false \
  -u $(oc whoami) -p $(oc whoami -t)
```

## First Deploy

From the repository root:

```bash
make build-image
make push-image
make deploy
```

`make deploy` processes the `postgresql-credentials.yaml` OpenShift Template, which auto-generates a random 16-character password. To override the defaults:

```bash
oc process -f deploy/openshift/postgresql-credentials.yaml \
  -p POSTGRESQL_USER=myuser \
  -p POSTGRESQL_PASSWORD=mypassword \
  -p POSTGRESQL_DATABASE=mydb | oc apply -f -
```

## Subsequent Deploys

```bash
make rollout
```

Rebuilds the image, pushes it, and does a rolling restart of the boss-coordinator deployment. PostgreSQL is untouched — data persists across rollouts.

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│ Namespace: jsell-agent-boss                                      │
│                                                                  │
│  ┌─────────────────┐  ┌──────────────────┐                       │
│  │ boss-coordinator │  │   postgresql     │                       │
│  │ (distroless)     │─►│   (RHEL sclorg)  │                       │
│  │ :8899            │  │   :5432          │                       │
│  └────────┬─────────┘  └──────────────────┘                       │
│           │                    │                                  │
│     ClusterIP              PVC 2Gi                               │
│     Service             postgresql-data                          │
│           │                                                      │
│     Route (edge TLS)                                             │
│     jsell-agent-boss.apps.okd1.timslab                           │
└───────────┼──────────────────────────────────────────────────────┘
            │
            │  AMBIENT_API_URL
            ▼
┌───────────────────────────────────────┐
│ Namespace: ambient-code               │
│  public-api (Ambient Code Platform)   │
│  https://public-api-ambient-code...   │
└───────────────────────────────────────┘
```

## Manifests

| File | Resources |
|------|-----------|
| `openshift/namespace.yaml` | Namespace `jsell-agent-boss` |
| `openshift/postgresql-credentials.yaml` | Template: PostgreSQL Secret (password auto-generated if not supplied) |
| `openshift/ambient-credentials.yaml` | Template: Ambient credentials Secret (`AMBIENT_TOKEN` required at deploy time) |
| `openshift/configmap.yaml` | ConfigMap: `DB_TYPE`, `DATA_DIR`, `COORDINATOR_PORT`, `AMBIENT_API_URL`, `AMBIENT_PROJECT` |
| `openshift/postgresql.yaml` | PostgreSQL Deployment + Service + PVC (2Gi) |
| `openshift/deployment.yaml` | boss-coordinator Deployment (from internal registry) |
| `openshift/service.yaml` | ClusterIP Service on port 8899 |
| `openshift/route.yaml` | Edge-terminated TLS Route |

## Dockerfile

Multi-stage build (`deploy/Dockerfile`):

1. **node:22-alpine** — builds Vue frontend (`npm ci && npm run build`)
2. **golang:1.24-alpine** — builds Go binary with `CGO_ENABLED=0` (pure-Go SQLite driver)
3. **gcr.io/distroless/static-debian12:nonroot** — minimal runtime with just the binary

## Ambient Backend

The deployment includes the Ambient Code Platform (ACP) session backend, which allows the coordinator to manage agents as remote Kubernetes pods via the ACP public API instead of local tmux sessions.

Configuration is split across the ConfigMap and a Secret:

- `AMBIENT_API_URL` and `AMBIENT_PROJECT` are in the ConfigMap (`boss-config`)
- `AMBIENT_TOKEN` is in the Secret (`ambient-credentials`), created via OpenShift Template

To supply the token at deploy time:

```bash
oc process -f deploy/openshift/ambient-credentials.yaml \
  -p AMBIENT_TOKEN=<your-bearer-token> | oc apply -f -
```

Since the distroless container has no tmux, the ambient backend becomes the default automatically. When both backends are available, tmux is the default unless unavailable.

## Notes

- PostgreSQL uses `registry.redhat.io/rhel10/postgresql-16:10.1` (sclorg). No `anyuid` SCC required — it handles OpenShift's arbitrary UID assignment natively.
- The boss-coordinator uses an `emptyDir` for `/data` (JSON/markdown cache). PostgreSQL is the source of truth; data survives pod restarts.
- The distroless container has no tmux binary. With `AMBIENT_API_URL` configured, the ambient backend is automatically selected as the default. Session lifecycle operations (`spawn`, `stop`, `restart`) use the ACP public API.
