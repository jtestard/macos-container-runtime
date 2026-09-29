#!/bin/sh
set -eu
PLIST="$HOME/Library/LaunchAgents/dev.macnative.macd.plist"
if [ -f "$PLIST" ]; then
  launchctl bootout "gui/$(id -u)" "$PLIST" || true
  rm "$PLIST"
fi
