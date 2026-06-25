# Examples

This directory contains example configurations for using the PullSecret Operator.

## Available Examples

### 1. ClusterPullSecret with Secret Reference
**File**: [clusterpullsecret-secretref.yaml](./clusterpullsecret-secretref.yaml)

Shows how to create a ClusterPullSecret that references an existing Kubernetes Secret. This is useful when you already have Docker credentials stored in a Secret.

### 2. ClusterPullSecret with Encrypted Data
**File**: [clusterpullsecret-encrypted.yaml](./clusterpullsecret-encrypted.yaml)

Demonstrates using encrypted credentials stored directly in the ClusterPullSecret resource. Best for GitOps workflows with tools like SOPS.

### 3. Deployment with Annotations
**File**: [deployment-example.yaml](./deployment-example.yaml)

Examples of Deployments that use the annotation to automatically get pull secrets from ClusterPullSecrets.

## Quick Start

1. **Install the operator** (see main [README](../README.md))

2. **Create a ClusterPullSecret** using one of the examples:
   ```bash
   kubectl apply -f clusterpullsecret-secretref.yaml
   ```

3. **Deploy an application** with the annotation:
   ```bash
   kubectl apply -f deployment-example.yaml
   ```

4. **Verify** the secret was created:
   ```bash
   kubectl get secrets -n <namespace>
   kubectl get clusterpullsecret
   ```

## Choosing the Right Method

### Use Secret Reference When:
- You already have credentials in a Kubernetes Secret
- You manage secrets through tools like External Secrets Operator or Vault
- You prefer traditional Kubernetes Secret management

### Use Encrypted Data When:
- You practice GitOps and want to commit encrypted credentials
- You use encryption tools like SOPS, Sealed Secrets, or similar
- You want to avoid managing separate Secret resources
- You need to version control your credentials securely

## Notes

- The operator requires appropriate RBAC permissions to read Secrets (when using secretRef) and to create/update/delete Secrets in target namespaces
- Secrets created by the operator have the annotation `pullsecret.itdpk.de/managed-by` to track ownership
- When using encrypted data, ensure you encrypt properly before base64 encoding
