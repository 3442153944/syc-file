#!/bin/bash
# Build the sync_core Rust core lib and package the artifact into sync_core/lib/ for Go cgo.
#
# Prereqs: Rust (cargo). On Linux the default host target (x86_64-unknown-linux-gnu) is used.
# Usage: run this from the sync_core dir:  ./build.sh

set -e
export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
root="$(cd "$(dirname "$0")" && pwd)"
cd "$root"

echo "==> cargo build --release"
cargo build --release

artifact="$root/target/release/libsync_core.a"
if [ ! -f "$artifact" ]; then
    echo "artifact not found: $artifact" >&2
    exit 1
fi

mkdir -p "$root/lib"
cp -f "$artifact" "$root/lib/libsync_core.a"
echo "==> packaged: $root/lib/libsync_core.a"

echo "==> native-static-libs (cgo LDFLAGS must cover these):"
RUSTFLAGS="--print native-static-libs" cargo rustc --release --lib 2>&1 | grep 'native-static-libs' || true
