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

#[derive(serde::Deserialize)]
pub struct InstallRequest {
    pub id: String,
    #[serde(default)]
    pub repo: String,
    #[serde(default)]
    pub expected_version: String,
    #[serde(default)]
    pub update: bool,
}

pub struct Installation {
    pub manifest: Manifest,
    pub changed: bool,
    pub reboot_from: Option<String>,
}

pub fn boot_id() -> Option<String> {
    let id = std::fs::read_to_string("/proc/sys/kernel/random/boot_id").ok()?;
    Some(id.trim().to_owned()).filter(|id| !id.is_empty())
}

fn reboot_origin(data_dir: &Path) -> Result<String, String> {
    #[cfg(target_os = "linux")]
    if data_dir == Path::new("/data/opt/jarvisd")
        && unsafe { libc::geteuid() } == 0
        && Path::new("/etc/init.d/S98jarvisd").is_file()
        && Path::new("/sbin/reboot").is_file()
        && Path::new("/system/vendor/quasar/launch.json").is_file()
        && let Some(id) = boot_id()
    {
        return Ok(id);
    }
    let _ = data_dir;
    Err("Для обновления плагина нужна перезагрузка колонки, здесь она недоступна".into())
}

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
    pub async fn install(self: &Arc<Self>, raw: &[u8], source: &str, official: bool) -> Result<Installation, String> {
        self.install_archive(raw, source, official, None).await
    }

    async fn install_archive(self: &Arc<Self>, raw: &[u8], source: &str, official: bool, request: Option<&InstallRequest>) -> Result<Installation, String> {
        let _g = self.install_lock.lock().await; // две установки сразу делили бы одну временную папку
        let (files, m) = unpack(raw)?;
        if let Some(request) = request {
            if (official || request.update) && m.id != request.id {
                return Err("В архиве другой плагин".into());
            }
            if !request.expected_version.is_empty() && m.version != request.expected_version {
                return Err(format!("В доступном архиве версия {}, запрошена {}. Обновите каталог и попробуйте снова.", m.version, request.expected_version));
            }
        }
        if m.kind == "module" && !(official && source == "core") {
            return Err("Rust-модуль выбирается из официального каталога".into());
        }
        if m.kind != "module" && super::modules::built_in(&m.id)
            && !(m.kind == "rust" && official && source == crate::DEFAULT_REPO && m.provides == [m.id.clone()])
        {
            return Err("Этот идентификатор зарезервирован для официального плагина".into());
        }
        let later = m.needs_newer_core();
        if later {
            return Err(format!("плагину нужен Джарвис {} или новее — сначала обновите Джарвиса", m.min_core));
        }
        let (previous, previous_state) = {
            let state = self.inner.lock().unwrap();
            (state.plugins.get(&m.id).map(|p| (p.manifest.clone(), p.err.clone())), state.state.get(&m.id).cloned())
        };
        let had = previous.is_some();
        let updating = request.is_some_and(|r| r.update);
        if updating && (!had || !previous_state.as_ref().is_some_and(|s| s.source.eq_ignore_ascii_case(source))) {
            return Err("Исходный плагин для обновления не найден".into());
        }
        if let Some((prior, _)) = &previous {
            if (updating || official) && super::newer(&prior.version, &m.version) {
                return Err(format!("На колонке уже стоит более новая версия {}", prior.version));
            }
            if updating && m.version == prior.version {
                return Ok(Installation { manifest: prior.clone(), changed: false, reboot_from: None });
            }
        }
        let reboot_from = if m.reboot_on_update && previous.as_ref().is_some_and(|(prior, _)| prior.version != m.version)
            && previous_state.as_ref().is_some_and(|state| state.enabled) {
            Some(reboot_origin(&self.data_dir)?)
        } else {
            None
        };
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
        let enabled = {
            let mut g = self.inner.lock().unwrap();
            g.plugins.insert(
                m.id.clone(),
                super::Plugin {
                    manifest: m.clone(),
                    dir: dst.clone(),
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
        if enabled && reboot_from.is_none() {
            self.start(&m.id).await;
        }
        if m.kind == "rust" && enabled && reboot_from.is_none() {
            let error = self.inner.lock().unwrap().plugins.get(&m.id)
                .filter(|p| !p.rt.as_ref().is_some_and(super::Runtime::running))
                .map(|p| p.err.clone());
            if let Some(error) = error {
                self.stop(&m.id).await;
                if bak.exists() {
                    replace_package(&bak, &dst, &tmp).map_err(|e| e.to_string())?;
                    let _ = std::fs::remove_dir_all(&tmp);
                } else {
                    std::fs::remove_dir_all(&dst).map_err(|e| e.to_string())?;
                }
                let restart = previous_state.as_ref().is_some_and(|state| state.enabled);
                {
                    let mut state = self.inner.lock().unwrap();
                    state.plugins.remove(&m.id);
                    if let Some((manifest, err)) = previous {
                        state.plugins.insert(m.id.clone(), super::Plugin { manifest, dir: dst, rt: None, err });
                    }
                    if let Some(prior) = previous_state {
                        state.state.insert(m.id.clone(), prior);
                    } else {
                        state.state.remove(&m.id);
                    }
                    self.save(&state);
                }
                if restart {
                    self.start(&m.id).await;
                }
                return Err(if error.is_empty() { "Процесс плагина завершился".into() } else { error });
            }
        }
        let _ = std::fs::remove_dir_all(&bak);
        log::info!("плагины: установлен {} {} ({source})", m.id, m.version);
        Ok(Installation { manifest: m, changed: true, reboot_from })
    }

    /// Скачать релиз репозитория GitHub и поставить. tag "" — последний релиз.
    /// Ищется jarvis-plugin-<arch>.zip, иначе jarvis-plugin.zip; для официальных — plugin-<id>[-arch].zip
    /// в релизе самого Джарвиса.
    pub async fn install_repo(self: &Arc<Self>, request: &InstallRequest) -> Result<Installation, String> {
        let repo = if request.repo.is_empty() { crate::DEFAULT_REPO } else { &request.repo };
        let id = request.id.as_str();
        if !super::catalog::valid_repo(repo) {
            return Err("репозиторий — вида owner/name".into());
        }
        let official = repo.eq_ignore_ascii_case(crate::DEFAULT_REPO);
        if official {
            if !valid_id(id) {
                return Err("не указан плагин".into());
            }
            let native = super::modules::built_in(id) || {
                let (items, _) = self.official_catalog(false).await;
                items.iter().any(|item| item["id"] == id && item["kind"] == "rust")
            };
            if native {
                let raw = download_official_rust(id).await?;
                return self.install_archive(&raw, crate::DEFAULT_REPO, true, Some(request)).await;
            }
        }
        #[cfg(feature = "lua")]
        if official {
            let raw = download_official_lua(id).await?;
            return self.install_archive(&raw, crate::DEFAULT_REPO, true, Some(request)).await;
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
        let rel_url = format!("{api}/repos/{repo}/releases/latest");
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
        self.install_archive(&raw, repo, official, Some(request)).await
    }
}

async fn download_official_rust(id: &str) -> Result<Vec<u8>, String> {
    let asset = format!("plugin-{id}-{}.zip", super::arch());
    let base = if let Some(base) = option_env!("JARVIS_PLUGIN_ASSETS") {
        base.to_owned()
    } else {
        let tag = format!("v{}", crate::version());
        format!("https://github.com/{}/releases/download/{tag}", crate::DEFAULT_REPO)
    };
    let sums = crate::net::download(&format!("{base}/SHA256SUMS"), 64 << 10).await
        .map_err(|error| format!("Пакеты для этой версии недоступны: {error}"))?;
    let sums = std::str::from_utf8(&sums).map_err(|_| "Неверный список контрольных сумм")?;
    let expected = jarvis_station::checksum(sums, &asset).ok_or("Для этой архитектуры нет официального пакета")?;
    let raw = crate::net::download(&format!("{base}/{asset}"), ZIP_MAX).await?;
    use sha2::{Digest, Sha256};
    if hex::encode(Sha256::digest(&raw)) != expected {
        return Err("Контрольная сумма официального пакета не совпала".into());
    }
    let (_, manifest) = unpack(&raw)?;
    if manifest.id != id || manifest.kind != "rust" {
        return Err("В архиве другой плагин".into());
    }
    Ok(raw)
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
