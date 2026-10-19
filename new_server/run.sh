#!/usr/bin/env bash
# Development launcher: verify/repair the native core before compiling Go.
set -euo pipefail
export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"
export CGO_ENABLED=1
export CC="${CC:-gcc}"
exec go run ./cmd/dev "$@"
