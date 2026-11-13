#!/usr/bin/env bash
set -euo pipefail
systemctl --user stop minecraft-lan-broadcaster-prototype.service
echo 'LAN connector stopped. The Minecraft game server is unaffected.'
