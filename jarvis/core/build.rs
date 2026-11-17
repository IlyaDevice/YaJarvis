// Панель вшивается в бинарник уже сжатой (gzip): браузер распаковывает сам, колонке — меньше места.
// Откуда брать файлы: JARVIS_UI, иначе jarvisd/ui в корне репозитория.

use std::{
    env, fs,
    io::Write,
    path::{Path, PathBuf},
};

fn walk(dir: &Path, base: &Path, out: &mut Vec<PathBuf>) {
    let mut entries: Vec<_> = fs::read_dir(dir).expect("папка панели").flatten().collect();
    entries.sort_by_key(|e| e.path());
    for e in entries {
        let p = e.path();
        if p.is_dir() {
            walk(&p, base, out);
        } else {
            out.push(p.strip_prefix(base).unwrap().to_path_buf());
        }
    }
}

fn main() {
    let manifest = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let ui = env::var("JARVIS_UI")
        .map(PathBuf::from)
        .unwrap_or_else(|_| manifest.join("../../jarvisd/ui"));
    println!("cargo:rerun-if-env-changed=JARVIS_UI");
    println!("cargo:rerun-if-changed={}", ui.display());
    let out = PathBuf::from(env::var("OUT_DIR").unwrap());
    let mut files = Vec::new();
    walk(&ui, &ui, &mut files);
    let mut table = String::from("pub static UI: &[(&str, &[u8])] = &[\n");
    for rel in files {
        let name = rel.to_string_lossy().replace('\\', "/");
        println!("cargo:rerun-if-changed={}", ui.join(&rel).display());
        let raw = fs::read(ui.join(&rel)).unwrap();
        let mut gz = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::best());
        gz.write_all(&raw).unwrap();
        let packed = gz.finish().unwrap();
        let dst = out.join("ui").join(format!("{name}.gz"));
        fs::create_dir_all(dst.parent().unwrap()).unwrap();
        fs::write(&dst, packed).unwrap();
        table.push_str(&format!(
            "    ({name:?}, include_bytes!({:?})),\n",
            dst.to_string_lossy().replace('\\', "/")
        ));
    }
    table.push_str("];\n");
    fs::write(out.join("ui_files.rs"), table).unwrap();
}
