//! Установка плагина: архив zip с компьютера или из релиза GitHub. Проверяем всё: только обычные файлы,
//! без выхода за папку, разумный размер, правильный plugin.json. Данные плагина переживают обновление.

use super::{Host, Manifest, PluginState, valid_id};
use std::{
    collections::BTreeMap,
    io::{Read, Write},
    path::Path,
    sync::Arc,
};

pub const ZIP_MAX: usize = 24 << 20;
pub const UNPACK_MAX: usize = 48 << 20;

/// Разобрать и проверить архив. Архив может лежать как есть или в одной папке верхнего уровня (как его делает GitHub).
pub fn unpack(raw: &[u8]) -> Result<(BTreeMap<String, Vec<u8>>, Manifest), String> {
    if raw.len() > ZIP_MAX {
        return Err(format!("архив плагина больше {} МБ", ZIP_MAX >> 20));
    }
    let mut zr = zip::ZipArchive::new(std::io::Cursor::new(raw)).map_err(|_| "это не zip-архив плагина".to_string())?;
    let mut files = BTreeMap::new();
    let mut total = 0usize;
    for i in 0..zr.len() {
        let mut f = zr.by_index(i).map_err(|e| e.to_string())?;
        let name = f.name().replace('\\', "/");
        let base = name.rsplit('/').next().unwrap_or("");
        if f.is_dir() || base.starts_with("__MACOSX") || name.contains("/__MACOSX/") || name.starts_with("__MACOSX/") {
            continue;
        }
        if f.is_symlink() || !f.is_file() {
            return Err(format!("в архиве не обычный файл: {name}"));
        }
        let clean_ok = !name.is_empty()
            && !name.starts_with('/')
            && name.split('/').all(|p| !p.is_empty() && p != "." && p != "..");
        if !clean_ok {
            return Err(format!("в архиве недопустимый путь: {name}"));
        }
        let mut b = Vec::new();
        f.by_ref().take((UNPACK_MAX - total + 1) as u64).read_to_end(&mut b).map_err(|e| e.to_string())?;
        total += b.len();
        if total > UNPACK_MAX {
            return Err(format!("плагин больше {} МБ в распакованном виде", UNPACK_MAX >> 20));
        }
        files.insert(name, b);
    }
    if !files.contains_key("plugin.json") {
        // всё в одной папке верхнего уровня — снимаем её
        let prefix = files
            .keys()
            .find(|n| n.ends_with("/plugin.json") && n.matches('/').count() == 1)
            .map(|n| n.trim_end_matches("plugin.json").to_string())
            .ok_or("в архиве нет plugin.json")?;
        files = files.into_iter().filter_map(|(n, b)| n.strip_prefix(&prefix).map(|r| (r.to_string(), b))).collect();
    }
    let m: Manifest = serde_json::from_slice(&files["plugin.json"]).map_err(|e| format!("plugin.json: {e}"))?;
    m.validate()?;
    if matches!(m.kind.as_str(), "lua" | "rust") && !files.contains_key(&m.main) {
        return Err(format!("в архиве нет {}", m.main));
    }
    if m.kind == "rust" {
        super::process::check_binary(&files[&m.main], &m.arch)?;
    }
    Ok((files, m))
}

pub fn free_mb(dir: &Path) -> u64 {
    #[cfg(unix)]
    {
        use std::os::unix::ffi::OsStrExt;
        let mut p = dir.as_os_str().as_bytes().to_vec();
        p.push(0);
        let mut s: libc::statvfs = unsafe { std::mem::zeroed() };
        if unsafe { libc::statvfs(p.as_ptr() as *const libc::c_char, &mut s) } == 0 {
            return (s.f_bavail as u64 * s.f_frsize as u64) >> 20;
        }
    }
    let _ = dir;
    0
}

impl Host {
    /// Поставить (или обновить) плагин из zip. source — owner/repo или file.
    pub async fn install(self: &Arc<Self>, raw: &[u8], source: &str, official: bool) -> Result<Manifest, String> {
        let _g = self.install_lock.lock().await; // две установки сразу делили бы одну временную папку
        let (files, m) = unpack(raw)?;
        if m.kind == "module" && !(official && source == "core") {
            return Err("Rust-модуль выбирается из официального каталога".into());
        }
        if m.kind == "rust" && (super::modules::built_in(&m.id) || super::modules::manifests().iter().any(|item| item.id == m.id)) {
            return Err("Этот идентификатор занят встроенным модулем".into());
        }
        let later = m.needs_newer_core();
        if later {
            return Err(format!("плагину нужен Джарвис {} или новее — сначала обновите Джарвиса", m.min_core));
        }
        let total: usize = files.values().map(Vec::len).sum();
        let free = free_mb(&self.data_dir);
        if free > 0 && free < (total >> 20) as u64 + 3 {
            return Err(format!("мало места на колонке: свободно {free} МБ, плагину нужно {} МБ", (total >> 20) + 1));
        }
        let dst = self.dir().join(&m.id);
        let tmp = self.dir().join(format!("{}.tmp", m.id));
        let _ = std::fs::remove_dir_all(&tmp);
        for (name, b) in &files {
            let p = tmp.join(name);
            std::fs::create_dir_all(p.parent().unwrap()).map_err(|e| e.to_string())?;
            let mode = if m.kind == "rust" && name == &m.main { 0o700 } else { 0o644 };
            if let Err(e) = crate::settings::write_atomic(&p, b, mode) {
                let _ = std::fs::remove_dir_all(&tmp);
                return Err(e.to_string());
            }
        }
        let had = self.inner.lock().unwrap().plugins.contains_key(&m.id);
        let bak = self.dir().join(format!("{}.old", m.id));
        if bak.exists() {
            std::fs::remove_dir_all(&bak).map_err(|e| e.to_string())?;
        }
        if had {
            self.stop(&m.id).await;
        }
        if let Err(error) = replace_package(&tmp, &dst, &bak) {
            if had {
                self.start(&m.id).await;
            }
            return Err(error.to_string());
        }
        let _ = std::fs::remove_dir_all(&bak);
        let enabled = {
            let mut g = self.inner.lock().unwrap();
            g.plugins.insert(
                m.id.clone(),
                super::Plugin {
                    manifest: m.clone(),
                    dir: dst,
                    rt: None,
                    err: if later { format!("заработает после обновления Джарвиса до {}", m.min_core) } else { String::new() },
                },
            );
            let st = g.state.entry(m.id.clone()).or_insert_with(PluginState::default);
            st.source = source.into();
            st.official = official;
            st.installed_at = super::now_rfc3339();
            if !had {
                st.enabled = true;
            }
            st.removed = false;
            let en = st.enabled;
            self.save(&g);
            en
        };
        if enabled && !later {
            self.start(&m.id).await;
        }
        log::info!("плагины: установлен {} {} ({source})", m.id, m.version);
        Ok(m)
    }

    /// Скачать релиз репозитория GitHub и поставить. tag "" — последний релиз.
    /// Ищется jarvis-plugin-<arch>.zip, иначе jarvis-plugin.zip; для официальных — plugin-<id>[-arch].zip
    /// в релизе самого Джарвиса.
    pub async fn install_repo(self: &Arc<Self>, repo: &str, id: &str, tag: &str) -> Result<Manifest, String> {
        if !super::catalog::valid_repo(repo) {
            return Err("репозиторий — вида owner/name".into());
        }
        let official = repo.eq_ignore_ascii_case(crate::DEFAULT_REPO);
        #[cfg(feature = "lua")]
        if official && tag.is_empty() {
            let raw = download_official_lua(id).await?;
            return self.install(&raw, crate::DEFAULT_REPO, true).await;
        }
        let names: Vec<String> = if official {
            if !valid_id(id) {
                return Err("не указан плагин".into());
            }
            vec![format!("plugin-{id}.zip")]
        } else {
            vec![format!("jarvis-plugin-{}.zip", super::arch()), "jarvis-plugin.zip".into()]
        };
        let api = super::catalog::github_api();
        let rel_url = if tag.is_empty() {
            format!("{api}/repos/{repo}/releases/latest")
        } else {
            format!("{api}/repos/{repo}/releases/tags/{tag}")
        };
        let rel = crate::net::get_json(&rel_url).await.map_err(|e| format!("релиз {repo}: {e}"))?;
        let assets = rel.get("assets").and_then(|a| a.as_array()).cloned().unwrap_or_default();
        let link = names.iter().find_map(|want| {
            assets.iter().find_map(|a| {
                let ok = a.get("name").and_then(|v| v.as_str()) == Some(want.as_str())
                    && a.get("size").and_then(|v| v.as_u64()).unwrap_or(0) as usize <= ZIP_MAX;
                ok.then(|| a.get("browser_download_url").and_then(|v| v.as_str()).unwrap_or("").to_string())
            })
        });
        let Some(link) = link.filter(|l| !l.is_empty()) else {
            return Err(format!("в последнем релизе {repo} нет {}", names.join(" или ")));
        };
        let raw = crate::net::download(&link, ZIP_MAX).await?;
        if official && unpack(&raw)?.1.id != id {
            return Err("В архиве другой плагин".into());
        }
        self.install(&raw, repo, official).await
    }
}

fn replace_package(tmp: &Path, dst: &Path, backup: &Path) -> std::io::Result<()> {
    if !dst.exists() {
        return std::fs::rename(tmp, dst);
    }
    std::fs::rename(dst, backup)?;
    let mut moved = Vec::new();
    let result = (|| {
        for name in ["data.json", "data"] {
            let old = backup.join(name);
            if !old.exists() {
                continue;
            }
            let staged = tmp.join(name);
            if staged.is_dir() {
                std::fs::remove_dir_all(&staged)?;
            } else if staged.exists() {
                std::fs::remove_file(&staged)?;
            }
            std::fs::rename(old, staged)?;
            moved.push(name);
        }
        std::fs::rename(tmp, dst)
    })();
    if result.is_err() {
        for name in moved {
            std::fs::rename(tmp.join(name), backup.join(name))?;
        }
        std::fs::rename(backup, dst)?;
        let _ = std::fs::remove_dir_all(tmp);
    }
    result
}

#[cfg(feature = "lua")]
async fn download_official_lua(id: &str) -> Result<Vec<u8>, String> {
    if !valid_id(id) { return Err("Не указан плагин".into()); }
    let manifest = crate::net::download(&super::catalog::official_url(&format!("{id}/plugin.json")), 64 << 10).await?;
    let m: Manifest = serde_json::from_slice(&manifest).map_err(|e| format!("plugin.json: {e}"))?;
    m.validate()?;
    if m.id != id || m.kind != "lua" || m.main != "main.lua" {
        return Err("Исходники не соответствуют запрошенному Lua-плагину".into());
    }
    let script = crate::net::download(&super::catalog::official_url(&format!("{id}/main.lua")), 512 << 10).await?;
    let mut archive = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
    for (name, text) in [("plugin.json", manifest), ("main.lua", script)] {
        archive.start_file(name, zip::write::SimpleFileOptions::default()).map_err(|e| e.to_string())?;
        archive.write_all(&text).map_err(|e| e.to_string())?;
    }
    Ok(archive.finish().map_err(|e| e.to_string())?.into_inner())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    pub fn make_zip(files: &[(&str, &str)]) -> Vec<u8> {
        let mut w = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
        for (n, b) in files {
            w.start_file(*n, zip::write::SimpleFileOptions::default()).unwrap();
            w.write_all(b.as_bytes()).unwrap();
        }
        w.finish().unwrap().into_inner()
    }

    #[test]
    fn unpacks_nested_and_rejects_bad() {
        let z = make_zip(&[("x/plugin.json", r#"{"id":"dice","name":"Кубик","kind":"lua","category":"fun","main":"main.lua"}"#), ("x/main.lua", "--")]);
        let (files, m) = unpack(&z).unwrap();
        assert_eq!(m.id, "dice");
        assert!(files.contains_key("main.lua"));
        let bad = make_zip(&[("plugin.json", r#"{"id":"dice","name":"К","kind":"lua","category":"fun","main":"main.lua"}"#), ("../evil", "x")]);
        assert!(unpack(&bad).unwrap_err().contains("недопустимый путь"));
        let nomain = make_zip(&[("plugin.json", r#"{"id":"dice","name":"К","kind":"lua","category":"fun","main":"main.lua"}"#)]);
        assert!(unpack(&nomain).unwrap_err().contains("нет main.lua"));
    }
}
