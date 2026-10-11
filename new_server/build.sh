#!/bin/bash
set -e
export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"
bash sync_core/build.sh
CGO_ENABLED=1 CC=gcc go build -o syc-file ./cmd
echo "==> built: $(pwd)/syc-file"
