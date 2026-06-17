# PullSecret Operator

A Kubernetes operator that manages cluster-wide pull secrets across namespaces automatically.

## Features

- **Cluster-wide Pull Secret Management**: Define a pull secret once at the cluster level (`ClusterPullSecret`) and have it materialized into a namespace on demand via Deployment annotations
- **Deployment Annotation Support**: Automatically create pull secrets for Deployments carrying the `cluster-pull-secrets` annotation
- **Multiple Credential Sources**: `secretRef`, `registryCredentials` (individual fields), or `encryptedData` (base64-encoded `.dockerconfigjson`)
- **Event-driven with periodic re-validation**: Watches ClusterPullSecrets and their source secrets, and re-validates every few minutes so credential rotation propagates
- **Monitoring-friendly status**: `status.phase` (`Synced`/`OutOfSync`) plus a Prometheus gauge `pullsecret_operator_clusterpullsecret_synced`
- **RBAC Compliant**: Minimal required permissions following security best practices

## Quick Start

### Prerequisites

- Kubernetes cluster (1.19+)
- Helm 3.0+
- kubectl configured to access your cluster

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
apiVersion: pullsecret.pascal-kutscha.de/v1
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
apiVersion: pullsecret.pascal-kutscha.de/v1
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

See [examples/clusterpullsecret-registrycredentials.yaml](examples/clusterpullsecret-registrycredentials.yaml)
for a full walkthrough.

Using `encryptedData` (base64-encoded `.dockerconfigjson`):

> **Note:** Despite its name, `encryptedData` is **not** decrypted by the operator —
> it is only base64-decoded, and the result must be valid `.dockerconfigjson`. It does
> **not** provide confidentiality (anyone with `get clusterpullsecrets` can decode it).
> For real encryption-at-rest, prefer `secretRef`/`registryCredentials` and protect the
> referenced Secret with SOPS, Sealed Secrets, or a secrets manager.

```yaml
apiVersion: pullsecret.pascal-kutscha.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    # base64 of a valid .dockerconfigjson document
    encryptedData: <base64-encoded-.dockerconfigjson>
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
    pullsecret.pascal-kutscha.de/cluster-pull-secrets: "my-registry,other-registry"
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
endless rollouts. The chart generates a self-signed serving certificate (reused across
upgrades). The default `failurePolicy: Ignore` means Pod creation is never blocked if the
operator is unavailable.

## Docker Image

The operator image is automatically built and pushed to GitHub Container Registry (GHCR) using GitHub Actions.

### Available Tags

- `latest` - Latest build from the main/master branch
- `<branch-name>` - Builds from feature branches
- `v<version>` - Tagged releases (e.g., `v1.0.0`)
- `<branch>-<sha>` - Specific commit builds

### Using the Image

```yaml
image: ghcr.io/pascalku/pullsecret-operator:latest
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
1. Generates the CRD from the Go types in `pkg/apis/pullsecret.pascal-kutscha.de/v1/`
2. Saves it to `pullsecret/templates/pullsecret.pascal-kutscha.de_clusterpullsecrets.yaml`
3. Copies it to `helm-chart/pullsecret-operator/templates/crd.yaml` for Helm deployment

**Important**: Always run `make manifests` after modifying the API types to ensure CRDs are up-to-date.

### Generate DeepCopy Methods

```bash
make generate
```

## CI/CD

The project includes a GitHub Actions workflow (`.github/workflows/docker-build-push.yaml`) that:

1. Builds the Docker image on every push to main/master
2. Pushes the image to GitHub Container Registry
3. Supports multi-architecture builds (linux/amd64, linux/arm64)
4. Creates appropriate tags based on branches and releases

## Helm Chart

The Helm chart is located in `helm-chart/pullsecret-operator/` and includes:

- Operator Deployment
- ServiceAccount with RBAC permissions
- ClusterRole and ClusterRoleBinding
- CustomResourceDefinition (CRD)
- Configurable values

See the [Helm chart README](helm-chart/pullsecret-operator/README.md) for detailed configuration options.

## Architecture

The operator consists of two main controllers:

1. **ClusterPullSecretReconciler**: Resolves and validates the credentials for each `ClusterPullSecret`, keeps every secret it manages (those carrying the managed-by annotation) up to date, watches the referenced source secrets so rotation propagates, re-validates periodically, and reports `status.phase`/metrics. It does **not** blanket-create secrets in every namespace.
2. **DeploymentReconciler**: Watches Deployments with the annotation, creates/updates the `pullsecret-<name>` secret in their namespace, and cleans it up when the annotation is removed (owner-reference based).

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
(blocked egress is treated as "inconclusive", not a failure). Validation re-runs on every
change and periodically (every 5 minutes).

## Configuration

Configuration options can be set via Helm values:

```yaml
# values.yaml
replicaCount: 1
image:
  repository: ghcr.io/pascalku/pullsecret-operator
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

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on:
- Making changes to the API
- Regenerating CRDs and generated code
- Testing your changes
- Kubebuilder markers reference

## License

See LICENSE file for details.

## Support

For issues, questions, or feature requests, please open an issue on GitHub.
