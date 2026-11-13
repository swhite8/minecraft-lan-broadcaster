#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "$(readlink -f -- "$0")")"
exec ./start.sh -target 192.0.2.10:19132 "$@"
