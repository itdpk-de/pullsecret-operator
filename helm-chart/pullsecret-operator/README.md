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
| `metrics.port` | Prometheus metrics port | `8080` |
| `metrics.service.enabled` | Create a Service for the metrics port | `true` |
| `serviceMonitor.enabled` | Create a Prometheus Operator ServiceMonitor | `false` |
| `serviceMonitor.interval` | Scrape interval | `30s` |
| `serviceMonitor.labels` | Extra labels for the ServiceMonitor | `{}` |
| `health.port` | Health/readiness probe port | `8081` |
| `registryValidation.enabled` | Active registry-login validation | `true` |
| `registryValidation.timeoutSeconds` | Per-request timeout | `10` |
| `registryValidation.intervalSeconds` | How often to re-check registry logins | `1800` |
| `registryValidation.caBundle.configMapName` | ConfigMap with a CA bundle for private registries | `""` |
| `registryValidation.caBundle.key` | Key in that ConfigMap | `ca.crt` |
| `webhook.enabled` | Enable the Pod imagePullSecrets injection webhook | `false` |
| `webhook.port` | Webhook server port | `9443` |
| `webhook.failurePolicy` | Webhook failure policy (`Ignore`/`Fail`) | `Ignore` |
| `webhook.certManager.enabled` | Use cert-manager for the serving cert (GitOps-friendly) | `false` |
| `webhook.namespaceSelector` | Limit which namespaces the webhook acts on | excludes `kube-system`/`kube-node-lease` |
| `podDisruptionBudget.enabled` | Create a PodDisruptionBudget | `false` |
| `podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `affinity` | Pod affinity (defaults to soft anti-affinity) | soft anti-affinity |

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

## Development

### CRD Generation

The CRD (`templates/crd.yaml`) is automatically generated from the Go API types using kubebuilder's controller-gen. 

**Do not manually edit `templates/crd.yaml`**. Instead:

1. Modify the API types in the main repository: `pkg/apis/pullsecret.pascal-kutscha.de/v1/clusterpullsecret_types.go`
2. Run `make manifests` from the repository root to regenerate the CRD
3. The CRD will be automatically copied to the Helm chart templates directory

## Support

For issues and questions, please visit the [GitHub repository](https://github.com/PascalKu/pullsecret-operator).
