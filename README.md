# ClusterPullSecret Operator

A Kubernetes operator that automatically manages Docker registry pull secrets across your cluster. This operator simplifies the distribution and synchronization of container registry credentials to workloads without manually creating secrets in every namespace.

## Problem Statement

In Kubernetes, accessing private container registries requires pull secrets in each namespace where pods need to pull images. This creates several challenges:

- **Manual secret distribution**: Creating and maintaining the same pull secret in multiple namespaces is tedious and error-prone
- **Credential rotation**: When registry credentials change, all secrets across all namespaces must be updated
- **Security**: Storing secrets in multiple places increases the attack surface
- **Onboarding**: New namespaces require manual secret provisioning

## Solution

The ClusterPullSecret Operator solves these problems by:

1. **Centralized management**: Define pull secrets once at the cluster level using the `ClusterPullSecret` CRD
2. **Automatic synchronization**: The operator automatically creates and updates secrets in namespaces where they're needed
3. **Annotation-based**: Workloads opt-in by adding a simple annotation to their Deployment
4. **Credential rotation**: Update the ClusterPullSecret once, and all derived secrets are automatically updated
5. **Multiple credential sources**: Supports both direct secret references and encrypted credential data

## Architecture

The operator consists of two main controllers:

### 1. ClusterPullSecret Controller
- Watches `ClusterPullSecret` custom resources (cluster-scoped)
- Synchronizes credentials to all managed secrets across the cluster
- Handles finalizers for cleanup when ClusterPullSecrets are deleted
- Updates the status to reflect sync state

### 2. Deployment Controller
- Watches `Deployment` resources with the annotation `pullsecret.pascal-kutscha.de/cluster-pull-secrets`
- Creates namespace-scoped secrets based on referenced ClusterPullSecrets
- Maintains owner references for proper garbage collection
- Updates secrets when ClusterPullSecret credentials change

## Installation

### Prerequisites
- Kubernetes cluster (v1.19+)
- `kubectl` configured to access your cluster
- Cluster admin permissions

### Step 1: Install the CRD

```bash
kubectl apply -f pullsecret/templates/pullsecret.pascal-kutscha.de_clusterpullsecrets.yaml
```

### Step 2: Deploy the Operator

Create a deployment manifest for the operator:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: pullsecret-operator-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pullsecret-operator
  namespace: pullsecret-operator-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pullsecret-operator
  template:
    metadata:
      labels:
        app: pullsecret-operator
    spec:
      serviceAccountName: pullsecret-operator
      containers:
      - name: operator
        image: pascalku/pullsecret-operator:latest
        imagePullPolicy: Always
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pullsecret-operator
  namespace: pullsecret-operator-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pullsecret-operator
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["pullsecret.pascal-kutscha.de"]
  resources: ["clusterpullsecrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["pullsecret.pascal-kutscha.de"]
  resources: ["clusterpullsecrets/status"]
  verbs: ["get", "update", "patch"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pullsecret-operator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: pullsecret-operator
subjects:
- kind: ServiceAccount
  name: pullsecret-operator
  namespace: pullsecret-operator-system
```

Apply the deployment:

```bash
kubectl apply -f deploy.yaml
```

## Usage

### Option 1: Using an Existing Secret (secretRef)

This method references an existing Kubernetes secret containing your Docker credentials.

#### Step 1: Create a source secret

```bash
kubectl create secret docker-registry my-registry-creds \
  --docker-server=registry.example.com \
  --docker-username=myuser \
  --docker-password=mypassword \
  --docker-email=user@example.com \
  -n kube-system
```

#### Step 2: Create a ClusterPullSecret

```yaml
apiVersion: pullsecret.pascal-kutscha.de/v1
kind: ClusterPullSecret
metadata:
  name: example-registry
spec:
  credentials:
    secretRef:
      name: my-registry-creds
      namespace: kube-system
      key: .dockerconfigjson
```

Apply it:

```bash
kubectl apply -f clusterpullsecret.yaml
```

### Option 2: Using Encrypted Data (encryptedData)

This method stores credentials directly in the ClusterPullSecret as base64-encoded data. For production use, encrypt the credentials using tools like SOPS, Sealed Secrets, or cloud KMS before base64 encoding.

#### Step 1: Prepare your Docker config

Create a `.dockerconfigjson` file:

```json
{
  "auths": {
    "registry.example.com": {
      "username": "myuser",
      "password": "mypassword",
      "email": "user@example.com",
      "auth": "bXl1c2VyOm15cGFzc3dvcmQ="
    }
  }
}
```

#### Step 2: Base64 encode the data

```bash
cat .dockerconfigjson | base64 -w 0
```

#### Step 3: Create a ClusterPullSecret

```yaml
apiVersion: pullsecret.pascal-kutscha.de/v1
kind: ClusterPullSecret
metadata:
  name: example-registry-encrypted
spec:
  credentials:
    encryptedData: "eyJhdXRocyI6eyJyZWdpc3RyeS5leGFtcGxlLmNvbSI6eyJ1c2VybmFtZSI6Im15dXNlciIsInBhc3N3b3JkIjoibXlwYXNzd29yZCIsImVtYWlsIjoidXNlckBleGFtcGxlLmNvbSIsImF1dGgiOiJiWGwxYzJWeU9tMTVjR0Z6YzNkdmNtUT0ifX19"
```

### Step 4: Annotate Your Deployment

Add the annotation to your Deployment to request the pull secret:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: production
  annotations:
    pullsecret.pascal-kutscha.de/cluster-pull-secrets: "example-registry"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      imagePullSecrets:
      - name: pullsecret-example-registry
      containers:
      - name: app
        image: registry.example.com/myuser/my-app:latest
        ports:
        - containerPort: 8080
```

**Note**: The operator creates a secret named `pullsecret-<clusterpullsecret-name>` in the deployment's namespace. Reference it in `imagePullSecrets`.

### Multiple ClusterPullSecrets

You can reference multiple ClusterPullSecrets using a comma-separated list:

```yaml
metadata:
  annotations:
    pullsecret.pascal-kutscha.de/cluster-pull-secrets: "registry-one,registry-two,registry-three"
spec:
  imagePullSecrets:
  - name: pullsecret-registry-one
  - name: pullsecret-registry-two
  - name: pullsecret-registry-three
```

## Verifying the Setup

### Check ClusterPullSecret status

```bash
kubectl get clusterpullsecrets
kubectl describe clusterpullsecret example-registry
```

Expected output:
```
Name:         example-registry
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  pullsecret.pascal-kutscha.de/v1
Kind:         ClusterPullSecret
...
Status:
  Observed Generation:  1
  Synced:               true
  Last Sync Time:       2024-01-15T10:30:00Z
```

### Check generated secrets

```bash
kubectl get secrets -n production | grep pullsecret
```

Expected output:
```
pullsecret-example-registry   kubernetes.io/dockerconfigjson   1      5m
```

### Verify secret content

```bash
kubectl get secret pullsecret-example-registry -n production -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d
```

## How It Works

### Workflow

1. **Admin creates ClusterPullSecret**: A cluster administrator defines a ClusterPullSecret with credentials (either via secretRef or encryptedData)

2. **Developer annotates Deployment**: A developer adds the annotation `pullsecret.pascal-kutscha.de/cluster-pull-secrets: "example-registry"` to their Deployment

3. **Operator creates secret**: The Deployment controller detects the annotation and creates a secret named `pullsecret-example-registry` in the Deployment's namespace

4. **Secret synchronization**: The ClusterPullSecret controller watches for changes and keeps all managed secrets synchronized

5. **Garbage collection**: When a Deployment is deleted, its owner-referenced secrets are automatically cleaned up

6. **Cleanup on deletion**: When a ClusterPullSecret is deleted, the controller removes all secrets it manages

### Annotations

- `pullsecret.pascal-kutscha.de/cluster-pull-secrets`: Comma-separated list of ClusterPullSecret names to use
- `pullsecret.pascal-kutscha.de/managed-by-cluster-pull-secret`: Internal annotation marking secrets as managed by a specific ClusterPullSecret

## Building and Development

### Prerequisites

- Go 1.25+
- Docker
- kubectl
- Access to a Kubernetes cluster

### Build the operator

```bash
go build -o bin/pullsecret-operator ./operator
```

### Run locally

```bash
./bin/pullsecret-operator
```

### Generate manifests

```bash
# Generate CRD manifests
make manifests

# Generate deepcopy code
make generate
```

### Build and push Docker image

```bash
make build-push
```

Or manually:

```bash
docker build -t your-registry/pullsecret-operator:latest .
docker push your-registry/pullsecret-operator:latest
```

## Security Considerations

### Credential Storage

- **secretRef method**: Credentials are stored in a regular Kubernetes Secret (base64-encoded, not encrypted)
- **encryptedData method**: You should encrypt credentials using:
  - [SOPS](https://github.com/mozilla/sops) with cloud KMS (AWS KMS, GCP KMS, Azure Key Vault)
  - [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets)
  - External secrets operators (e.g., External Secrets Operator)

### RBAC

The operator requires cluster-wide permissions to:
- Read/write Secrets across all namespaces
- Read Deployments across all namespaces
- Manage ClusterPullSecret resources

Ensure the operator runs in a dedicated namespace with minimal privileges beyond what's required.

### Best Practices

1. **Use encryptedData with encryption**: Don't store plaintext credentials in encryptedData
2. **Rotate credentials regularly**: Update the ClusterPullSecret when credentials change
3. **Limit access to ClusterPullSecrets**: Use RBAC to control who can create/modify ClusterPullSecrets
4. **Monitor operator logs**: Watch for sync failures or credential issues
5. **Use separate ClusterPullSecrets per registry**: Don't combine multiple registries in one ClusterPullSecret

## Troubleshooting

### Secret not created

**Problem**: The secret `pullsecret-<name>` doesn't appear in the namespace.

**Solutions**:
1. Check that the annotation is correctly spelled: `pullsecret.pascal-kutscha.de/cluster-pull-secrets`
2. Verify the ClusterPullSecret exists: `kubectl get clusterpullsecret`
3. Check operator logs: `kubectl logs -n pullsecret-operator-system deployment/pullsecret-operator`
4. Verify the Deployment controller is watching: Check for reconciliation events

### ClusterPullSecret status shows "synced: false"

**Problem**: The ClusterPullSecret status indicates it's not synced.

**Solutions**:
1. Check the `reason` field: `kubectl get clusterpullsecret <name> -o yaml`
2. Common reasons:
   - `source-secret-not-found`: The referenced secret doesn't exist
   - `source-secret-key-missing`: The key doesn't exist in the secret
   - `invalid-encrypted-data`: The encryptedData is not valid base64
   - `no-credentials`: Neither secretRef nor encryptedData is provided

### Credentials not updating

**Problem**: Changed credentials in the source secret but pods still fail to pull images.

**Solutions**:
1. Wait for the reconciliation loop (usually within 10-60 seconds)
2. Force a sync by updating the ClusterPullSecret annotation
3. Restart pods to pick up the new secret data: `kubectl rollout restart deployment/<name>`

### Permission denied errors

**Problem**: Operator logs show permission errors.

**Solutions**:
1. Verify the ServiceAccount, ClusterRole, and ClusterRoleBinding are created
2. Check RBAC permissions: `kubectl auth can-i list secrets --as=system:serviceaccount:pullsecret-operator-system:pullsecret-operator -A`

## Uninstallation

### Step 1: Delete all ClusterPullSecrets

```bash
kubectl delete clusterpullsecrets --all
```

This triggers cleanup of all managed secrets.

### Step 2: Delete the operator

```bash
kubectl delete deployment pullsecret-operator -n pullsecret-operator-system
kubectl delete serviceaccount pullsecret-operator -n pullsecret-operator-system
kubectl delete clusterrole pullsecret-operator
kubectl delete clusterrolebinding pullsecret-operator
kubectl delete namespace pullsecret-operator-system
```

### Step 3: Delete the CRD

```bash
kubectl delete crd clusterpullsecrets.pullsecret.pascal-kutscha.de
```

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## License

This project is provided as-is without warranty. See the license file for details.

## Support

For questions or issues, please open an issue on the GitHub repository.
