#!/usr/bin/env bash
# Smoke test: install the operator into a throwaway kind cluster the way a user
# would (image -> kind load -> kubectl apply -k config/default) and check that it
# reconciles a CompatibilityPolicy.
#
#   make e2e                    create the cluster, test, delete the cluster
#   KEEP_CLUSTER=1 make e2e     leave the cluster running afterwards (the next
#                               run still starts from a fresh cluster)
#   E2E_SKIP_BUILD=1 make e2e   use an already built $IMG (CI builds it with buildx)
#
# Needs docker, kind, kubectl and curl. The cluster gets its own kubeconfig in
# bin/, so your current kube-context is never touched.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-viltrumite-e2e}"
UI_LOCAL_PORT="${UI_LOCAL_PORT:-8082}"
# The tag config/default deploys; kind load makes it available without a registry.
readonly IMG=ghcr.io/jeikeibnaa/kube-viltrumite:dev
readonly NAMESPACE=viltrumite-system
readonly DEPLOYMENT=viltrumite-controller-manager
readonly LEASE=kube-viltrumite.kubeviltrumite.io
readonly POLICY_FILE=config/samples/compatibilitypolicy_sample.yaml
readonly POLICY_NAMESPACE=default
readonly POLICY_NAME=test-policy

cd "$(dirname "$0")/.."
mkdir -p bin
# Relative on purpose: Git Bash rewrites absolute /c/... paths for Windows tools.
export KUBECONFIG=bin/e2e-kubeconfig

log() { printf '\n==> %s\n' "$*"; }
fail() {
  printf '\nFAIL: %s\n' "$*" >&2
  exit 1
}

for tool in docker kind kubectl curl; do
  command -v "$tool" >/dev/null || fail "$tool is not installed"
done

PF_PID=""
cleanup() {
  local status=$?
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" 2>/dev/null || true
  fi
  if [[ $status -ne 0 ]]; then
    log "Diagnostics"
    kubectl get pods -A -o wide || true
    kubectl -n "$NAMESPACE" describe deployment "$DEPLOYMENT" || true
    kubectl -n "$NAMESPACE" describe pods || true
    kubectl -n "$NAMESPACE" logs "deployment/$DEPLOYMENT" --tail=200 || true
    kubectl -n "$POLICY_NAMESPACE" get compatibilitypolicy "$POLICY_NAME" -o yaml || true
    kubectl get events -A --sort-by=.lastTimestamp | tail -30 || true
  fi
  if [[ "${KEEP_CLUSTER:-}" == "1" ]]; then
    log "Keeping cluster $CLUSTER_NAME (KUBECONFIG=$KUBECONFIG)"
  else
    log "Deleting cluster $CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME" || true
  fi
  exit "$status"
}
trap cleanup EXIT

log "kind cluster $CLUSTER_NAME"
# Always start fresh: a kept cluster would still run the previous :dev image,
# because re-loading the same tag does not restart the pod.
clusters=$(kind get clusters 2>/dev/null)
if grep -qx "$CLUSTER_NAME" <<<"$clusters"; then
  kind delete cluster --name "$CLUSTER_NAME"
fi
kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait 120s

if [[ "${E2E_SKIP_BUILD:-}" == "1" ]]; then
  log "Using the existing image $IMG"
  docker image inspect "$IMG" >/dev/null || fail "E2E_SKIP_BUILD=1 but $IMG is not in the local docker images"
else
  log "Build $IMG"
  make docker-build IMG="$IMG"
fi

log "Load $IMG into kind"
kind load docker-image "$IMG" --name "$CLUSTER_NAME"

log "Deploy (kubectl apply -k config/default)"
make deploy
kubectl -n "$NAMESPACE" rollout status "deployment/$DEPLOYMENT" --timeout=180s

log "Apply $POLICY_FILE"
kubectl wait --for=condition=Established crd/compatibilitypolicies.kubeviltrumite.io --timeout=60s
kubectl apply -f "$POLICY_FILE"

# The sample sets no trackedTools, so the reconciler must report discovery mode.
log "Wait for .status.mode"
kubectl -n "$POLICY_NAMESPACE" wait "compatibilitypolicy/$POLICY_NAME" \
  --for=jsonpath='{.status.mode}'=discovery --timeout=120s
kubectl -n "$POLICY_NAMESPACE" get compatibilitypolicy "$POLICY_NAME"
last_scan=$(kubectl -n "$POLICY_NAMESPACE" get compatibilitypolicy "$POLICY_NAME" -o jsonpath='{.status.lastScanTime}')
[[ -n "$last_scan" ]] || fail ".status.lastScanTime is empty"
echo "tracked tools: $(kubectl -n "$POLICY_NAMESPACE" get compatibilitypolicy "$POLICY_NAME" \
  -o jsonpath='{range .status.tools[*]}{.name}={.message}; {end}')"

log "Leader election"
pod=$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/component=controller-manager \
  -o jsonpath='{.items[0].metadata.name}') || fail "no controller-manager pod in $NAMESPACE"
holder=$(kubectl -n "$NAMESPACE" get lease "$LEASE" -o jsonpath='{.spec.holderIdentity}') ||
  fail "leader-election lease $NAMESPACE/$LEASE does not exist"
# client-go writes "<hostname>_<uuid>"; the hostname is the pod name.
[[ "$holder" == "${pod}_"* ]] || fail "lease $NAMESPACE/$LEASE is held by '$holder', not by pod $pod"
echo "lease $NAMESPACE/$LEASE held by $holder"

log "Operator log"
logs=$(kubectl -n "$NAMESPACE" logs "pod/$pod")
# RBAC gaps show up as "forbidden"; a path the read-only root FS does not cover
# shows up as EROFS.
if bad=$(grep -iE 'forbidden|read-only file system' <<<"$logs"); then
  printf '%s\n' "$bad" >&2
  fail "operator log has RBAC or read-only filesystem errors"
fi
if errors=$(grep -E '(^|[[:space:]])ERROR([[:space:]]|$)' <<<"$logs"); then
  printf 'warning: operator logged errors (not fatal for the smoke test):\n%s\n' "$errors"
fi
echo "no 'forbidden' or 'read-only file system' in $(wc -l <<<"$logs") log lines"

log "UI server through port-forward (it listens on the pod's 127.0.0.1:8082 only)"
kubectl -n "$NAMESPACE" port-forward "pod/$pod" "$UI_LOCAL_PORT:8082" >bin/e2e-port-forward.log 2>&1 &
PF_PID=$!
health=""
for _ in $(seq 1 30); do
  if health=$(curl -fsS "http://127.0.0.1:$UI_LOCAL_PORT/api/health" 2>/dev/null); then
    break
  fi
  sleep 1
done
[[ -n "$health" ]] || { cat bin/e2e-port-forward.log >&2; fail "GET /api/health did not answer"; }
echo "/api/health: $health"
grep -q '"status":"ok"' <<<"$health" || fail "unexpected /api/health response"
index=$(curl -fsS "http://127.0.0.1:$UI_LOCAL_PORT/")
grep -q '<div id="root">' <<<"$index" || fail "GET / did not return the UI's index.html"
echo "/ serves the UI"

log "PASS"
