#!/bin/sh
set -eu

PREFIX=${MACNATIVE_PREFIX:-"$HOME/Library/Application Support/macnative"}
SOCKET=${MACNATIVE_SOCKET:-/private/tmp/macnative-docker.sock}
LABEL=dev.macnative.macd
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
UID_NUMBER=$(id -u)

if [ ! -x "$PREFIX/bin/macd" ] || [ ! -x "$PREFIX/bin/imgrun" ]; then
  printf '%s\n' 'Run ./install.sh first.' >&2
  exit 1
fi
mkdir -p "$HOME/Library/LaunchAgents" "$PREFIX/logs" "$PREFIX/images"
xml() { printf '%s' "$1" | sed -e 's/\&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g'; }
MACD=$(xml "$PREFIX/bin/macd")
RUNNER=$(xml "$PREFIX/bin/imgrun")
STORE=$(xml "$PREFIX/images")
SOCKET_XML=$(xml "$SOCKET")
STDOUT=$(xml "$PREFIX/logs/macd.stdout.log")
STDERR=$(xml "$PREFIX/logs/macd.stderr.log")
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array>
    <string>$MACD</string><string>-runner</string><string>$RUNNER</string>
    <string>-store</string><string>$STORE</string>
    <string>-socket</string><string>$SOCKET_XML</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$STDOUT</string>
  <key>StandardErrorPath</key><string>$STDERR</string>
</dict></plist>
EOF
chmod 600 "$PLIST"
plutil -lint "$PLIST"
launchctl bootout "gui/$UID_NUMBER" "$PLIST" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$UID_NUMBER" "$PLIST"
printf 'macd is loaded as %s; socket: %s\n' "$LABEL" "$SOCKET"
