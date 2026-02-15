# PullSecret Operator Helm Chart

This Helm chart deploys the PullSecret Operator, which manages cluster-wide pull secrets across Kubernetes namespaces.

## Features

- Automatically syncs pull secrets to namespaces based on `ClusterPullSecret` custom resources
- Watches Deployments with specific annotations to create pull secrets on-demand
- Supports both encrypted credentials and secret references
- RBAC-compliant with minimal required permissions

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+

## Installing the Chart

To install the chart with the release name `pullsecret-operator`:

```bash
helm install pullsecret-operator ./helm-chart/pullsecret-operator \
  --namespace pullsecret-operator-system \
  --create-namespace
```

## Configuration

The following table lists the configurable parameters of the chart and their default values.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/pascalku/pullsecret-operator` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `image.tag` | Image tag | `latest` |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name | `""` (generated) |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `256Mi` |
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |
| `namespace` | Namespace for operator deployment | `pullsecret-operator-system` |
| `installCRDs` | Install CRDs | `true` |
| `leaderElection.enabled` | Enable leader election | `false` |

## Usage

After installing the chart, you can create a `ClusterPullSecret` resource:

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

Or use encrypted credentials:

```yaml
apiVersion: pullsecret.pascal-kutscha.de/v1
kind: ClusterPullSecret
metadata:
  name: my-registry
spec:
  credentials:
    encryptedData: <base64-encoded-encrypted-credentials>
```

## Uninstalling the Chart

To uninstall/delete the `pullsecret-operator` deployment:

```bash
helm uninstall pullsecret-operator --namespace pullsecret-operator-system
```

## Support

For issues and questions, please visit the [GitHub repository](https://github.com/PascalKu/pullsecret-operator).
