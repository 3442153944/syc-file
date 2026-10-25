#!/usr/bin/env bash
# Legacy entry point. Always build the canonical repository-root sys_core.
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
core="$(cd "$root/../../sys_core" && pwd)"
bash "$core/build.sh" "$@"
# Only legacy consumers need this name. Current Go code links sys_core directly.
mkdir -p "$root/lib"
cp "$core/lib/libsys_core.a" "$root/lib/libsync_core.a"
echo "==> legacy compatibility copy: $root/lib/libsync_core.a"
