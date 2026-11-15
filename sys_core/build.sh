#!/usr/bin/env bash
# Canonical native C ABI build. Extra arguments (for example --offline) go to Cargo.
set -euo pipefail
export PATH="$HOME/.cargo/bin:$PATH"
root="$(cd "$(dirname "$0")" && pwd)"
cd "$root"
target="$(rustc -vV | sed -n 's/^host: //p')"
[[ -n "$target" ]] || { echo 'Cannot determine Rust host target' >&2; exit 1; }
target_dir="${CARGO_TARGET_DIR:-$root/target}"
[[ "$target_dir" = /* ]] || target_dir="$root/$target_dir"
echo "==> shared sys_core: $root ($target)"
cargo build --manifest-path "$root/Cargo.toml" --release --lib --locked \
    --target "$target" --target-dir "$target_dir" "$@"
artifact="$target_dir/$target/release/libsys_core.a"
[[ -f "$artifact" ]] || { echo "Missing shared core artifact: $artifact" >&2; exit 1; }
mkdir -p "$root/lib"
temporary="$(mktemp "$root/lib/.libsys_core.XXXXXX")"
trap 'rm -f "$temporary"' EXIT
cp "$artifact" "$temporary"
mv -f "$temporary" "$root/lib/libsys_core.a"
echo "==> shared C ABI: $root/lib/libsys_core.a"
