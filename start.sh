#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "$(readlink -f -- "$0")")"
if ! systemctl --user is-active --quiet minecraft-lan-broadcaster-prototype.service; then
  systemd-run --user --unit=minecraft-lan-broadcaster-prototype --collect \
    "$PWD/dist/minecraft-lan-broadcaster" "$@"
fi
echo 'LAN connector logs. Press Ctrl+C or close this terminal to stop the connector.'
trap 'systemctl --user stop minecraft-lan-broadcaster-prototype.service >/dev/null 2>&1 || true' EXIT
journalctl --user -u minecraft-lan-broadcaster-prototype.service -n 15 -f --no-pager
