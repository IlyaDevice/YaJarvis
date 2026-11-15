"""Collect dependency license texts from the Go module cache (no credentials)."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import urllib.request

root = Path(__file__).resolve().parent.parent
dest = root / "LICENSES"
dest.mkdir(exist_ok=True)
data = subprocess.check_output([os.environ.get("GO", "go"), "list", "-m", "-json", "all"], cwd=root / "jarvisd", text=True)
decoder = json.JSONDecoder()
while data.strip():
    mod, pos = decoder.raw_decode(data.lstrip())
    data = data.lstrip()[pos:]
    if mod.get("Main") or not mod.get("Dir"):
        continue
    for source in Path(mod["Dir"]).iterdir():
        if source.is_file() and source.name.upper().startswith(("LICENSE", "COPYING", "NOTICE")):
            name = mod["Path"].replace("/", "_") + "__" + source.name
            shutil.copyfile(source, dest / name)
shutil.copyfile(Path(subprocess.check_output([os.environ.get("GO", "go"), "env", "GOROOT"], text=True).strip()) / "LICENSE", dest / "Go-LICENSE")
for name, url in {
    "Manrope-OFL.txt": "https://raw.githubusercontent.com/google/fonts/main/ofl/manrope/OFL.txt",
    "openWakeWord-LICENSE": "https://raw.githubusercontent.com/dscripka/openWakeWord/main/LICENSE",
}.items():
    urllib.request.urlretrieve(url, dest / name)
print("Collected dependency licenses:", len(list(dest.iterdir())))
