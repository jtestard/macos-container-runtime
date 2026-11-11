#!/bin/sh
# Create (or resume) a Kind cluster and join this Mac to it as a native node.
#
# Usage: scripts/kube-up.sh [cluster-name]        (default: macnative)
#
# The cluster runs in Docker Desktop; mackube runs as a launchd job that
# restarts after a crash but is never started at login. Run this script again
# after a reboot. scripts/kube-down.sh removes everything it creates.
#
# Optional environment:
#   KIND_CONFIG          Kind cluster config file (for example the voice cluster's)
#   MACKUBE_NODE         node name (default: macnative)
#   MACKUBE_SLOT         optional macnative.dev/slot node label
#   MACKUBE_PORT         virtual kubelet HTTPS port (default: 10250)
#   MACD_SOCKET          macd socket (default: /private/tmp/macnative-docker.sock)
#   KIND_DOCKER_CONTEXT  Docker context that runs Kind (default: desktop-linux)
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
NAME=${1:-macnative}
case "$NAME" in
  *[!a-z0-9-]* | '' | -*) printf 'Invalid cluster name %s: use lowercase letters, digits and dashes.\n' "$NAME" >&2; exit 2 ;;
esac
PREFIX=${MACNATIVE_PREFIX:-"$HOME/Library/Application Support/macnative"}
STATE="$PREFIX/kube/$NAME"
NODE=${MACKUBE_NODE:-macnative}
SLOT=${MACKUBE_SLOT:-}
PORT=${MACKUBE_PORT:-10250}
SOCKET=${MACD_SOCKET:-/private/tmp/macnative-docker.sock}
KIND_DOCKER_CONTEXT=${KIND_DOCKER_CONTEXT:-desktop-linux}
LABEL="dev.macnative.mackube.$NAME"
UID_NUMBER=$(id -u)
KUBECONFIG_FILE="$STATE/kubeconfig"

fail() { printf 'kube-up: %s\n' "$*" >&2; exit 1; }
for tool in docker kind kubectl go; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is not installed."
done
kind_() { DOCKER_CONTEXT="$KIND_DOCKER_CONTEXT" kind "$@"; }

# 1. Both container runtimes must be up: macd runs native Pods, Docker Desktop runs Kind.
docker -H "unix://$SOCKET" version >/dev/null 2>&1 \
  || fail "macd is not reachable at $SOCKET. Start it with scripts/enable-macd.sh."
docker --context "$KIND_DOCKER_CONTEXT" info >/dev/null 2>&1 \
  || fail "Docker context $KIND_DOCKER_CONTEXT is not reachable. Start Docker Desktop."

# 2. Create the cluster, or start its node containers again after a reboot.
if kind_ get clusters 2>/dev/null | grep -qx "$NAME"; then
  printf 'Cluster %s exists; making sure its nodes are running.\n' "$NAME"
  for container in $(kind_ get nodes --name "$NAME"); do
    docker --context "$KIND_DOCKER_CONTEXT" start "$container" >/dev/null
  done
else
  printf 'Creating Kind cluster %s in Docker Desktop.\n' "$NAME"
  if [ -n "${KIND_CONFIG:-}" ]; then
    kind_ create cluster --name "$NAME" --config "$KIND_CONFIG" --wait 120s
  else
    kind_ create cluster --name "$NAME" --wait 120s
  fi
fi

# 3. Save a private single-context kubeconfig for mackube and for you.
mkdir -p "$STATE"
chmod 700 "$STATE"
(umask 077 && kind_ get kubeconfig --name "$NAME" > "$KUBECONFIG_FILE")
i=0
until kubectl --kubeconfig "$KUBECONFIG_FILE" get --raw /readyz >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -le 60 ] || fail "the API server of cluster $NAME did not become ready."
  sleep 2
done

# 4. Build mackube.
mkdir -p "$PREFIX/bin"
GOCACHE=${GOCACHE:-"$ROOT/.build/go-cache"} go -C "$ROOT/kube" build -o "$PREFIX/bin/mackube" ./cmd/mackube

# 5. (Re)start the launchd job. A restart adopts native Pods that are still running.
launchctl bootout "gui/$UID_NUMBER/$LABEL" >/dev/null 2>&1 || true
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  fail "port $PORT is already in use (another cluster's mackube?). Set MACKUBE_PORT to a free port."
fi
xml() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g'; }
SLOT_ARGS=""
[ -z "$SLOT" ] || SLOT_ARGS="<string>-slot</string><string>$(xml "$SLOT")</string>"
PLIST="$STATE/mackube.plist"
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array>
    <string>$(xml "$PREFIX/bin/mackube")</string>
    <string>-kubeconfig</string><string>$(xml "$KUBECONFIG_FILE")</string>
    <string>-kind-cluster</string><string>$(xml "$NAME")</string>
    <string>-node-name</string><string>$(xml "$NODE")</string>
    $SLOT_ARGS
    <string>-socket</string><string>$(xml "$SOCKET")</string>
    <string>-kubelet-address</string><string>host.docker.internal</string>
    <string>-kubelet-port</string><string>$(xml "$PORT")</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$(xml "$STATE/mackube.log")</string>
  <key>StandardErrorPath</key><string>$(xml "$STATE/mackube.log")</string>
</dict></plist>
EOF
chmod 600 "$PLIST"
plutil -lint "$PLIST" >/dev/null
# The plist stays out of ~/Library/LaunchAgents, so launchd never loads it at login.
launchctl bootstrap "gui/$UID_NUMBER" "$PLIST"
cat > "$STATE/env" <<EOF
NODE='$NODE'
SOCKET='$SOCKET'
EOF

# 6. Wait for the Mac node.
i=0
until [ "$(kubectl --kubeconfig "$KUBECONFIG_FILE" get node "$NODE" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]; do
  i=$((i + 1))
  if [ "$i" -gt 60 ]; then
    tail -n 20 "$STATE/mackube.log" >&2 || true
    fail "node $NODE did not become Ready; full log: $STATE/mackube.log"
  fi
  sleep 1
done

cat <<EOF

Cluster $NAME is up and node $NODE is Ready.
  kubeconfig:  $KUBECONFIG_FILE
  kubectl:     kubectl --context kind-$NAME ...  (or --kubeconfig the file above)
  mackube log: $STATE/mackube.log
  stop:        scripts/kube-down.sh $NAME
EOF
