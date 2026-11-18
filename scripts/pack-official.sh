#!/bin/sh
# One independently installable archive per official plugin and architecture.
set -eu
out=${1:?output directory}
arch=${2:?station architecture}
target=${3:?Rust target}
release=${4:?target release directory}
cd "$(dirname "$0")/.."
for id in $(python3 -c 'import json; print(*(p["id"] for p in json.load(open("plugins/index.json", encoding="utf-8"))["plugins"] if p["kind"] == "rust"))'); do
    (cd jarvis && cargo build --locked --release --target "$target" -p jarvis-official-plugin --features "$id")
    python3 scripts/pack-official.py "$out" "$arch" "$id" "$release/jarvis-official-plugin"
done
