"""Write an architecture-specific official package from its executable."""
import json
import pathlib
import sys
import zipfile

out, arch, plugin_id, binary = sys.argv[1:]
metadata_dir = "vpn-rust" if plugin_id == "vpn" else plugin_id
manifest = json.loads(pathlib.Path("plugins", metadata_dir, "plugin.json").read_text(encoding="utf-8"))
if manifest["id"] != plugin_id or manifest["kind"] != "rust" or manifest["provides"] != [plugin_id]:
    raise SystemExit(f"Unexpected manifest for {plugin_id}")
manifest["arch"] = arch
with zipfile.ZipFile(pathlib.Path(out, f"plugin-{plugin_id}-{arch}.zip"), "w", zipfile.ZIP_DEFLATED) as package:
    package.writestr("plugin.json", json.dumps(manifest, ensure_ascii=False))
    package.write(binary, manifest["main"])
