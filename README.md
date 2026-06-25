# PullSecret Operator

A Kubernetes operator that manages cluster-wide pull secrets across namespaces automatically.

## Features

- **Cluster-wide pull secret management**: Define a pull secret once at the cluster level (`ClusterPullSecret`) and have it materialized into a namespace on demand
- **Works with all built-in workloads**: Deployment, StatefulSet, DaemonSet, ReplicaSet, Job and CronJob carrying the `cluster-pull-secrets` annotation
- **Multiple credential sources**: `secretRef`, `registryCredentials` (individual fields), or inline `dockerConfigJson`
- **Active credential validation**: structural checks plus an optional registry login to catch expired/wrong credentials
- **Monitoring-friendly**: `status.phase` (`Synced`/`OutOfSync`), Kubernetes Events, and Prometheus metrics
- **Optional mutating webhook**: injects `imagePullSecrets` into Pods (GitOps-safe — no reconciliation loops)
- **RBAC compliant**: minimal required permissions

## Quick Start

### Prerequisites

- Kubernetes cluster 1.25+ (the CRD uses CEL validation; on older clusters the
  runtime checks still apply but the CRD-level `oneOf` is not enforced)
- Helm 3.0+
- kubectl configured to access your cluster
- Optional: [cert-manager](https://cert-manager.io) if you enable the webhook with
  `webhook.certManager.enabled=true`

### Installation

Install the operator using Helm:

```bash
helm install pullsecret-operator ./helm-chart/pullsecret-operator \
  --namespace pullsecret-operator-system \
  --create-namespace
```

### Usage

#### Create a ClusterPullSecret

Using a secret reference:

```yaml
apiVersion: pullsecret.itdpk.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    secretRef:
      namespace: default
      name: my-docker-credentials
      key: .dockerconfigjson
```

Using individual registry credential fields (`registryCredentials`):

The operator reads the individual fields (`server`, `username`, `password`,
`email`) from a referenced Secret and assembles a valid `.dockerconfigjson`
automatically. No credential data is embedded in the ClusterPullSecret spec.

```yaml
apiVersion: pullsecret.itdpk.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    registryCredentials:
      secretName: my-registry-credentials
      namespace: default
      # Optional — these are the defaults and can be omitted when your
      # Secret uses the same key names.
      serverKey: server
      usernameKey: username
      passwordKey: password
      emailKey: email
```

Using inline `dockerConfigJson` (raw JSON **or** base64-encoded — both accepted):

> **Note:** Inline credentials are **not** encrypted and provide no confidentiality
> (anyone with `get clusterpullsecrets` can read them). For real encryption-at-rest,
> prefer `secretRef`/`registryCredentials` and protect the referenced Secret with SOPS,
> Sealed Secrets, or a secrets manager.
>
> The legacy field `encryptedData` is a **deprecated** alias for `dockerConfigJson`
> (treated identically, never decrypted). Use `dockerConfigJson`.

```yaml
apiVersion: pullsecret.itdpk.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    # raw .dockerconfigjson or base64 of it
    dockerConfigJson: <.dockerconfigjson-or-base64>
```

Apply the resource:

```bash
kubectl apply -f clusterpullsecret.yaml
```

#### Annotate a workload

Add the annotation to a workload to automatically get its pull secret. Supported
kinds: **Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob**. Multiple
ClusterPullSecrets can be listed comma-separated.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    pullsecret.itdpk.de/cluster-pull-secrets: "my-registry,other-registry"
spec:
  # ... workload spec
```

The operator creates a secret named `pullsecret-<clusterpullsecret-name>` in the
workload's namespace. Reference it via `imagePullSecrets`. When the annotation is
removed, the secret is cleaned up (owner-reference based).

#### Optional: automatic injection via mutating webhook

Instead of referencing `imagePullSecrets` manually, you can enable a mutating webhook
that injects them into **Pods** at admission, based on the annotation on the Pod or its
owning workload:

```bash
helm upgrade --install pullsecret-operator ./helm-chart/pullsecret-operator \
  --set webhook.enabled=true
```

Because it mutates the **Pod** and not the Deployment/CR, the change is invisible to
GitOps controllers (Argo CD / Flux) and does **not** cause reconciliation loops or
endless rollouts. The default `failurePolicy: Ignore` means Pod creation is never blocked
if the operator is unavailable, and the webhook ignores `kube-system`/`kube-node-lease` by
default (`webhook.namespaceSelector`).

> **GitOps + certificate:** by default the chart generates a self-signed serving cert and
> reuses it via Helm `lookup`. Tools that render with `helm template` (Argo CD/Flux) have
> no cluster `lookup`, so the cert/`caBundle` would regenerate on every sync. For those,
> set `webhook.certManager.enabled=true` to manage the cert via **cert-manager** (CA is
> injected into the webhook config), which is fully GitOps-stable.

For HA, run multiple replicas with `leaderElection.enabled=true` and
`podDisruptionBudget.enabled=true` (replicas spread via the default soft anti-affinity).
Only the elected leader runs the controllers; the webhook, metrics and health endpoints
serve on every replica. The leader-election Lease lives in the operator's own namespace by
default (override with `leaderElection.namespace`), and the leader releases it on graceful
shutdown so a standby takes over immediately instead of waiting for the lease to expire.

## Docker Image

The operator image is automatically built and pushed to GitHub Container Registry (GHCR) using GitHub Actions.

### Available Tags

- `latest` - Latest build from the main/master branch
- `<branch-name>` - Builds from feature branches
- `v<version>` - Tagged releases (e.g., `v1.0.0`)
- `<branch>-<sha>` - Specific commit builds

### Using the Image

```yaml
image: ghcr.io/itdpk-de/pullsecret-operator:latest
```

## Development

### Building Locally

Build the Docker image:

```bash
docker build -t pullsecret-operator:dev .
```

Or use the Makefile to build and push to GHCR:

```bash
# Build locally (loads into local docker)
make docker-build

# Build and push to GitHub Container Registry (requires docker login to ghcr.io)
make build-push

# Or with custom values
make build-push REGISTRY=ghcr.io IMAGE_REPO=yourorg/pullsecret-operator IMAGE_TAG=v1.0.0
```

**Note**: The Makefile uses `docker buildx` for builds. If your tag starts with a hyphen (e.g., from CI), it will be automatically sanitized.

### Building from Source

```bash
go build -o manager ./operator/main.go
```

### Generate CRDs

The CRDs are automatically generated using kubebuilder's controller-gen tool. To regenerate them:

```bash
make manifests
```

This command:
1. Generates the CRD from the Go types in `pkg/apis/pullsecret.itdpk.de/v1/`
2. Saves it to `pullsecret/templates/pullsecret.itdpk.de_clusterpullsecrets.yaml`
3. Copies it to `helm-chart/pullsecret-operator/templates/crd.yaml` for Helm deployment

**Important**: Always run `make manifests` after modifying the API types to ensure CRDs are up-to-date.

### Generate DeepCopy Methods

```bash
make generate
```

## CI/CD

Two GitHub Actions workflows run on push and pull requests:

- **`.github/workflows/ci.yaml`** — `go vet`/`build`/`test` (incl. envtest integration tests), a CRD/deepcopy drift check, and `helm lint`/`helm template`.
- **`.github/workflows/docker-build-push.yaml`** — builds the image (`linux/amd64`) and pushes it to GHCR on pushes to the default branch and on tags, with tags derived from the branch/semver/SHA.

## Helm Chart

The Helm chart is located in `helm-chart/pullsecret-operator/` and includes:

- Operator Deployment
- ServiceAccount with RBAC permissions
- ClusterRole and ClusterRoleBinding
- CustomResourceDefinition (CRD)
- Configurable values

See the [Helm chart README](helm-chart/pullsecret-operator/README.md) for detailed configuration options.

## Architecture

The operator consists of:

1. **ClusterPullSecretReconciler**: Resolves and validates the credentials for each `ClusterPullSecret`, keeps every secret it manages (those carrying the managed-by annotation) up to date, and re-validates periodically (so source-secret rotation propagates within the resync interval), reporting `status.phase`/metrics/events. It only caches the secrets it manages — source secrets are read uncached — and does **not** blanket-create secrets in every namespace.
2. **WorkloadReconciler** (one per kind: Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob): Watches annotated workloads, creates/updates the `pullsecret-<name>` secret in their namespace, recreates it if deleted out-of-band, and cleans it up when the annotation is removed (owner-reference based).
3. **Pod mutating webhook** (optional): injects `imagePullSecrets` into Pods based on the annotation on the Pod or its owning workload.

## Monitoring & credential validation

Each `ClusterPullSecret` reports a status you can scrape and alert on:

```bash
kubectl get clusterpullsecrets
# NAME          PHASE       SYNCED   REASON                  LAST-SYNC   AGE
# my-registry   Synced      true                             30s         5m
# broken        OutOfSync   false    source-secret-not-found              5m
```

Prometheus metrics (served on the metrics port, default `:8080`):

- `pullsecret_operator_clusterpullsecret_synced{clusterpullsecret}` — `1` synced, `0` out of sync
- `pullsecret_operator_registry_auth_ok{clusterpullsecret,registry}` — `1` registry accepted the credentials, `0` rejected them

Example alerts:

```promql
pullsecret_operator_clusterpullsecret_synced == 0
pullsecret_operator_registry_auth_ok == 0
```

**Validation depth:** the operator always validates structurally (source secret exists,
fields present, content is valid `.dockerconfigjson`) and, by default, **actively logs in**
to each referenced registry to catch expired/wrong credentials. The active check needs
network egress from the operator; disable it with `registryValidation.enabled: false`
(blocked egress is treated as "inconclusive", not a failure). Structural validation
re-runs on every change and every ~5 minutes; the active registry login is throttled to
`registryValidation.intervalSeconds` (default 30 minutes) to avoid registry rate limits.
It honors `HTTP(S)_PROXY` and can trust a private CA via `registryValidation.caBundle`.

> The active check is best-effort: registries that issue anonymous tokens even for invalid
> credentials may report `OK`. Treat `registry_auth_ok == 0` as a reliable failure signal,
> and `1` as "not definitively broken".

Scrape the metrics with the bundled `Service` (`metrics.service.enabled`, default on) or a
`ServiceMonitor` (`serviceMonitor.enabled`, requires the Prometheus Operator).

### Events

The operator emits Kubernetes Events for traceability (`kubectl describe` / `kubectl get events`):

On the **ClusterPullSecret**:
- `Normal Updated` — a managed secret was updated
- `Normal Synced` — credentials became valid again (on recovery)
- `Warning CredentialError` — credentials could not be resolved (e.g. source secret/key missing, invalid `.dockerconfigjson`)
- `Warning RegistryAuthFailed` — a registry rejected the credentials (lists the registries)

On the **workload** (Deployment/StatefulSet/…):
- `Normal Created` / `Normal Updated` — pull secret created/updated in the namespace
- `Normal Deleted` / `Normal Released` — pull secret cleaned up / ownership released when the annotation is removed
- `Warning CredentialError` / `Warning ClusterPullSecretNotFound` — the referenced ClusterPullSecret could not be used

Failure events are emitted on state transitions (not every resync), so a persistent failure
is reported once rather than repeatedly. Inconclusive "registry unreachable" results are
intentionally not evented (to avoid flapping noise) — they remain visible in
`status.registryAuth` and the metrics.

## Configuration

Configuration options can be set via Helm values:

```yaml
# values.yaml
replicaCount: 1
image:
  repository: ghcr.io/itdpk-de/pullsecret-operator
  tag: latest
resources:
  limits:
    cpu: 500m
    memory: 256Mi
```

## Security

- The operator runs as a non-root user (UID 65532)
- Read-only root filesystem
- Capabilities dropped
- Minimal RBAC permissions

## Contributing

When changing the API types in `pkg/apis/pullsecret.itdpk.de/v1/`, always regenerate
the generated code and CRDs and run the tests:

```bash
make generate manifests   # DeepCopy methods + CRD
go test ./...             # set KUBEBUILDER_ASSETS (envtest) for the integration tests
```

The CI enforces a CRD/deepcopy drift check, so commit the regenerated files.

## License

© itdpk.de. A license file will be added before public release.

## Support

For issues, questions, or feature requests, please open an issue on GitHub.
