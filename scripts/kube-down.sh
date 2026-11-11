#!/bin/sh
# Remove a cluster created by scripts/kube-up.sh: stop the native Pods on the
# Mac node, stop mackube, delete the Kind cluster, and delete its local state.
#
# Usage: scripts/kube-down.sh [cluster-name]      (default: macnative)
set -eu

NAME=${1:-macnative}
case "$NAME" in
  *[!a-z0-9-]* | '' | -*) printf 'Invalid cluster name %s.\n' "$NAME" >&2; exit 2 ;;
esac
PREFIX=${MACNATIVE_PREFIX:-"$HOME/Library/Application Support/macnative"}
STATE="$PREFIX/kube/$NAME"
KIND_DOCKER_CONTEXT=${KIND_DOCKER_CONTEXT:-desktop-linux}
LABEL="dev.macnative.mackube.$NAME"
KUBECONFIG_FILE="$STATE/kubeconfig"
NODE=macnative
SOCKET=/private/tmp/macnative-docker.sock
# shellcheck disable=SC1091
[ ! -f "$STATE/env" ] || . "$STATE/env"
kind_() { DOCKER_CONTEXT="$KIND_DOCKER_CONTEXT" kind "$@"; }
k() { kubectl --kubeconfig "$KUBECONFIG_FILE" "$@"; }

CLUSTER_EXISTS=false
if kind_ get clusters 2>/dev/null | grep -qx "$NAME"; then CLUSTER_EXISTS=true; fi

# 1. Stop native Pods while mackube is still running, so it stops their processes.
UIDS=""
if $CLUSTER_EXISTS && [ -f "$KUBECONFIG_FILE" ] && k get node "$NODE" >/dev/null 2>&1; then
  k cordon "$NODE" >/dev/null
  UIDS=$(k get pods -A --field-selector "spec.nodeName=$NODE" \
    -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}')
  if [ -n "$UIDS" ]; then
    printf 'Stopping native Pods on node %s.\n' "$NODE"
    if launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
      k delete pods -A --field-selector "spec.nodeName=$NODE" --wait=true --timeout=60s || true
    else
      # Nobody can confirm the deletion; step 3 removes the processes instead.
      k delete pods -A --field-selector "spec.nodeName=$NODE" --wait=false >/dev/null || true
    fi
  fi
fi

# 2. Stop mackube.
if launchctl bootout "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
  printf 'Stopped mackube (%s).\n' "$LABEL"
fi

# 3. If mackube was not running, remove the Pods' processes from macd directly.
for uid in $UIDS; do
  # docker rm -f succeeds even for a missing container, so check first.
  if docker -H "unix://$SOCKET" inspect "kube-$uid" >/dev/null 2>&1; then
    docker -H "unix://$SOCKET" rm -f "kube-$uid" >/dev/null
    printf 'Removed leftover native process kube-%s.\n' "$uid"
  fi
done

# 4. Delete the cluster and the local state.
if $CLUSTER_EXISTS; then
  kind_ delete cluster --name "$NAME"
fi
rm -rf "$STATE"
printf 'Cluster %s is down.\n' "$NAME"
