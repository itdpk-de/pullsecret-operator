#!/usr/bin/env bash
#
# Reusable end-to-end test: spins up a kind cluster, builds & loads the operator
# image, installs the Helm chart (with the mutating webhook enabled), and verifies
# the full flow on a real cluster:
#   1. ClusterPullSecret -> generated pullsecret-<name> Secret in the app namespace
#   2. mutating webhook injects imagePullSecrets into the Pod
#   3. ClusterPullSecret reports status.phase=Synced
#
# Requirements: docker, kind, kubectl, helm. By default the script creates and
# deletes its own kind cluster. Set KEEP_CLUSTER=1 to keep it for debugging, or
# USE_EXISTING_CLUSTER=1 to run against the current kubectl context (no kind).
set -euo pipefail

CLUSTER="${CLUSTER:-pullsecret-e2e}"
IMAGE_REPO="${IMAGE_REPO:-pullsecret-operator}"
IMAGE_TAG="${IMAGE_TAG:-e2e}"
IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"
OPERATOR_NS="${OPERATOR_NS:-pullsecret-operator-system}"
APP_NS="${APP_NS:-pullsecret-e2e-app}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mE2E FAILED: %s\033[0m\n' "$*" >&2; exit 1; }

# Wait until `cmd` prints `expected` (trimmed), or fail after ~timeout seconds.
wait_for() {
  local desc="$1" expected="$2" timeout="${3:-120}" cmd="$4" got=""
  log "waiting for: ${desc}"
  for ((i = 0; i < timeout; i += 3)); do
    got="$(eval "$cmd" 2>/dev/null || true)"
    [[ "$got" == "$expected" ]] && { echo "  ok (${got})"; return 0; }
    sleep 3
  done
  fail "${desc} (last value: '${got}', wanted '${expected}')"
}

cleanup() {
  [[ "${KEEP_CLUSTER:-}" == "1" || "${USE_EXISTING_CLUSTER:-}" == "1" ]] && return 0
  log "deleting kind cluster ${CLUSTER}"
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for bin in docker kubectl helm; do command -v "$bin" >/dev/null || fail "missing required tool: $bin"; done

if [[ "${USE_EXISTING_CLUSTER:-}" != "1" ]]; then
  command -v kind >/dev/null || fail "missing required tool: kind"
  log "creating kind cluster ${CLUSTER}"
  kind create cluster --name "$CLUSTER" --wait 120s

  log "building operator image ${IMAGE}"
  docker build -t "$IMAGE" "$REPO_ROOT"
  log "loading image into kind"
  kind load docker-image "$IMAGE" --name "$CLUSTER"
fi

log "installing Helm chart (webhook enabled, registry validation off for determinism)"
helm upgrade --install pullsecret-operator "$REPO_ROOT/helm-chart/pullsecret-operator" \
  -n "$OPERATOR_NS" --create-namespace \
  --set image.repository="$IMAGE_REPO" \
  --set image.tag="$IMAGE_TAG" \
  --set image.pullPolicy=IfNotPresent \
  --set webhook.enabled=true \
  --set registryValidation.enabled=false \
  --wait --timeout 180s

log "creating fixtures in ${APP_NS}"
kubectl create namespace "$APP_NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$APP_NS" delete secret ghcr-creds >/dev/null 2>&1 || true
kubectl -n "$APP_NS" create secret generic ghcr-creds \
  --from-literal=server=ghcr.io \
  --from-literal=username=e2e-user \
  --from-literal=password=e2e-pass

kubectl apply -f - <<EOF
apiVersion: pullsecret.itdpk.de/v1
kind: ClusterPullSecret
metadata:
  name: ghcr
spec:
  credentials:
    registryCredentials:
      secretName: ghcr-creds
      namespace: ${APP_NS}
EOF

# Operator (and its webhook) is ready (helm --wait). Now create the annotated workload
# so its Pod is admitted after the webhook is serving.
kubectl -n "$APP_NS" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  annotations:
    pullsecret.itdpk.de/cluster-pull-secrets: "ghcr"
spec:
  replicas: 1
  selector: { matchLabels: { app: web } }
  template:
    metadata: { labels: { app: web } }
    spec:
      containers:
      - name: web
        image: ghcr.io/itdpk-de/does-not-exist:latest
EOF

# 1. generated secret of the right type
wait_for "generated pull secret exists" "kubernetes.io/dockerconfigjson" 120 \
  "kubectl -n $APP_NS get secret pullsecret-ghcr -o jsonpath='{.type}'"

# 2. webhook injected imagePullSecrets into the Pod
wait_for "webhook injected imagePullSecrets into the Pod" "pullsecret-ghcr" 120 \
  "kubectl -n $APP_NS get pods -l app=web -o jsonpath='{.items[0].spec.imagePullSecrets[0].name}'"

# 3. ClusterPullSecret reports Synced
wait_for "ClusterPullSecret status is Synced" "Synced" 120 \
  "kubectl get clusterpullsecret ghcr -o jsonpath='{.status.phase}'"

# 4. cleanup is owner-reference based: deleting the workload garbage-collects the secret.
# (Note: a Deployment propagates its annotations to its ReplicaSet, so the RS keeps its
# own owner reference until the Deployment — and thus the RS — is deleted.)
log "verifying owner-reference cleanup on workload deletion"
kubectl -n "$APP_NS" delete deployment web --wait=false
wait_for "generated pull secret is garbage-collected" "" 120 \
  "kubectl -n $APP_NS get secret pullsecret-ghcr -o name 2>/dev/null"

printf '\n\033[1;32mE2E PASSED\033[0m\n'
