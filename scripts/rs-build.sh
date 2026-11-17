#!/bin/sh
# Сборка ядра на Rust под колонку (aarch64, статически, musl).
#   scripts/rs-build.sh [версия]      → build/jarvis-arm64
# Нужно: rustup с целью aarch64-unknown-linux-musl. Компоновщик — встроенный rust-lld (jarvis/.cargo/config.toml).
# C-код (когда появится: Lua, Opus) собирает zig: CC_aarch64_unknown_linux_musl="zig cc -target aarch64-linux-musl".
set -eu
cd "$(dirname "$0")/.."
VER="${1:-dev}"
export JARVIS_VERSION="$VER"
export JARVIS_UI="${JARVIS_UI:-$PWD/jarvisd/ui}"
if command -v zig >/dev/null 2>&1; then
	export CC_aarch64_unknown_linux_musl="zig cc -target aarch64-linux-musl"
	export AR_aarch64_unknown_linux_musl="zig ar"
fi
(cd jarvis && cargo build --release --target aarch64-unknown-linux-musl)
mkdir -p build
cp jarvis/target/aarch64-unknown-linux-musl/release/jarvis build/jarvis-arm64
ls -l build/jarvis-arm64
