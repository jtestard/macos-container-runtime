#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PREFIX=${MACNATIVE_PREFIX:-"$HOME/Library/Application Support/macnative"}
SOCKET=${MACNATIVE_SOCKET:-/private/tmp/macnative-docker.sock}
mkdir -p "$ROOT/.build" "$PREFIX/bin" "$PREFIX/images"
GOCACHE=${GOCACHE:-"$ROOT/.build/go-cache"}
export GOCACHE

cd "$ROOT"
go build -o "$ROOT/.build/imgrun" ./cmd/imgrun
go build -o "$ROOT/.build/macd" ./cmd/macd
cp "$ROOT/.build/imgrun" "$PREFIX/bin/imgrun"
cp "$ROOT/.build/macd" "$PREFIX/bin/macd"
chmod 700 "$PREFIX/bin/imgrun" "$PREFIX/bin/macd"

if command -v docker >/dev/null 2>&1; then
  if ! docker context inspect macnative >/dev/null 2>&1; then
    docker context create macnative --docker "host=unix://$SOCKET"
  else
    EXISTING_SOCKET=$(docker context inspect macnative --format '{{.Endpoints.docker.Host}}')
    if [ "$EXISTING_SOCKET" != "unix://$SOCKET" ]; then
      printf 'Existing macnative context points to %s; expected unix://%s\n' "$EXISTING_SOCKET" "$SOCKET" >&2
      printf 'Update it with: docker context update macnative --docker "host=unix://%s"\n' "$SOCKET" >&2
    fi
  fi
else
  printf '%s\n' 'Docker CLI not found; install it, then create the macnative context.' >&2
fi

printf 'Installed macd and imgrun in %s/bin\n' "$PREFIX"
printf 'Start macd now with: "%s/bin/macd" -runner "%s/bin/imgrun" -store "%s/images" -socket "%s"\n' "$PREFIX" "$PREFIX" "$PREFIX" "$SOCKET"
printf '%s\n' 'For automatic startup, run scripts/enable-macd.sh.'
