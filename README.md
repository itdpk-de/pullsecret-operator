# PullSecret Operator

A Kubernetes operator that manages cluster-wide pull secrets across namespaces automatically.

## Features

- **Cluster-wide Pull Secret Management**: Define pull secrets once at the cluster level and have them automatically synced to all namespaces
- **Deployment Annotation Support**: Automatically create pull secrets for Deployments with specific annotations
- **Multiple Credential Sources**: Support for both encrypted credentials and secret references
- **Event-driven**: Watches for changes and automatically reconciles secrets
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

Using encrypted data:

```yaml
apiVersion: pullsecret.pascal-kutscha.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    encryptedData: <base64-encoded-encrypted-credentials>
```

Apply the resource:

```bash
kubectl apply -f clusterpullsecret.yaml
```

#### Annotate a Deployment

Add the annotation to your Deployment to automatically get pull secrets:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    pullsecret.pascal-kutscha.de/cluster-pull-secrets: "my-registry"
spec:
  # ... deployment spec
```

The operator will automatically create a secret named `pullsecret-my-registry` in the same namespace as the Deployment.

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

1. **ClusterPullSecretReconciler**: Manages cluster-wide pull secrets and syncs them to all secrets with the managed annotation
2. **DeploymentReconciler**: Watches Deployments with the annotation and creates/updates pull secrets in their namespaces

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
