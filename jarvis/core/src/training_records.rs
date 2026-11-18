//! The optional training plugin owns the recording buffer and local dataset.
use crate::{
    settings::{rand_hex, write_atomic},
    training_background::Recorder,
};
use flate2::{Compression, read::GzDecoder, write::GzEncoder};
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    fs,
    io::{Cursor, Read, Write},
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant, SystemTime},
};

const RATE: usize = 16_000;
const MAX_CLIP: u64 = 6 << 20;
const MAX_ARCHIVE: u64 = 32 << 20;
pub const MAX_LIMIT_MB: i64 = 5;
const KINDS: [&str; 4] = ["pos", "false", "near", "bg"];
const CLIPS: &str = "rust-clips";
const OLD_CLIPS: &str = "clips";
const GZIP: &[u8] = b"\x1f\x8b";

#[derive(Clone, Serialize)]
pub struct Clip {
    pub kind: String,
    pub name: String,
    pub reviewed: bool,
    pub size: u64,
    pub time: String,
    #[serde(skip)]
    storage: &'static str,
}

#[derive(Default)]
struct Observe {
    near: Option<Instant>,
    background: Option<Instant>,
}

pub struct Records {
    root: PathBuf,
    files: Mutex<()>,
    sample: Mutex<Observe>,
    pub background: Arc<Recorder>,
    write_pending: AtomicBool,
    stopped: AtomicBool,
}

impl Records {
    pub fn new(root: PathBuf) -> Self {
        let background = Arc::new(Recorder::new(root.join("background-cloud.json")));
        Self {
            root,
            files: Mutex::new(()),
            sample: Mutex::default(),
            background,
            write_pending: false.into(),
            stopped: false.into(),
        }
    }

    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        self.background.close();
    }

    pub fn sample_kind(&self, hit: bool, prob: f32, loud: bool) -> Option<&'static str> {
        if hit {
            return Some("pos");
        }
        let mut s = self.sample.lock().unwrap();
        if (0.35..0.99).contains(&prob)
            && s.near
                .is_none_or(|at| at.elapsed() >= Duration::from_secs(20))
        {
            s.near = Some(Instant::now());
            return Some("near");
        }
        if loud
            && prob < 0.15
            && s.background
                .is_none_or(|at| at.elapsed() >= Duration::from_secs(420))
        {
            s.background = Some(Instant::now());
            return Some("bg");
        }
        None
    }

    pub fn save_soon(
        self: &Arc<Self>,
        rt: &tokio::runtime::Handle,
        kind: &'static str,
        pcm: Vec<i16>,
        prob: f32,
        limit_mb: i64,
    ) {
        if self.write_pending.swap(true, Ordering::AcqRel) {
            return;
        }
        let records = self.clone();
        rt.spawn_blocking(move || {
            if let Err(error) = records.save(kind, &pcm, prob, limit_mb) {
                log::warn!("запись для обучения: {error}");
            }
            records.write_pending.store(false, Ordering::Release);
        });
    }

    fn path(&self, storage: &str, kind: &str, name: &str) -> Result<PathBuf, String> {
        if !KINDS.contains(&kind) || !valid_name(name) {
            return Err("Неверное имя записи".into());
        }
        Ok(self.root.join(storage).join(kind).join(name))
    }

    fn locate(&self, kind: &str, name: &str) -> Result<(PathBuf, &'static str), String> {
        for storage in [CLIPS, OLD_CLIPS] {
            let path = self.path(storage, kind, name)?;
            if path.symlink_metadata().is_ok_and(|meta| meta.is_file()) {
                return Ok((path, storage));
            }
        }
        Err("Запись не найдена".into())
    }

    fn list_dir(&self, storage: &'static str) -> Vec<Clip> {
        let mut output = Vec::new();
        for kind in KINDS {
            let Ok(entries) = fs::read_dir(self.root.join(storage).join(kind)) else {
                continue;
            };
            for entry in entries.flatten() {
                let Ok(name) = entry.file_name().into_string() else {
                    continue;
                };
                if !valid_name(&name) || !entry.file_type().is_ok_and(|t| t.is_file()) {
                    continue;
                }
                let Ok(meta) = entry.metadata() else { continue };
                if meta.len() > MAX_CLIP {
                    continue;
                }
                let changed = meta.modified().unwrap_or(SystemTime::UNIX_EPOCH);
                let time = jiff::Timestamp::try_from(changed)
                    .unwrap_or(jiff::Timestamp::UNIX_EPOCH)
                    .to_string();
                output.push(Clip {
                    kind: kind.into(),
                    reviewed: name.ends_with("_r.wav"),
                    name,
                    size: meta.len(),
                    time,
                    storage,
                });
            }
        }
        output
    }

    fn list(&self) -> Vec<Clip> {
        let mut clips = self.list_dir(CLIPS);
        clips.extend(self.list_dir(OLD_CLIPS));
        clips.sort_by(|a, b| b.time.cmp(&a.time).then_with(|| b.name.cmp(&a.name)));
        clips
    }

    pub fn snapshot(&self, mode: bool, limit: i64, previous: bool) -> Value {
        let _lock = self.files.lock().unwrap();
        let clips = self.list();
        let counts = KINDS.map(|kind| (kind, clips.iter().filter(|c| c.kind == kind).count()));
        let used = |storage| clips.iter().filter(|c| c.storage == storage).map(|c| c.size).sum::<u64>() >> 10;
        json!({"mode":mode,"limit_mb":limit.clamp(1,MAX_LIMIT_MB),"prev_model":previous,
            "used_kb":used(CLIPS),"legacy_kb":used(OLD_CLIPS),"local":clips.len(),
            "counts":counts.into_iter().collect::<std::collections::BTreeMap<_,_>>(),"clips":clips.iter().take(400).collect::<Vec<_>>()})
    }

    fn save(&self, kind: &str, pcm: &[i16], prob: f32, limit_mb: i64) -> Result<(), String> {
        let _lock = self.files.lock().unwrap();
        if self.stopped.load(Ordering::Acquire) {
            return Ok(());
        }
        let wav = pcm_wav(pcm);
        let compressed = compress(&wav).map_err(|e| e.to_string())?;
        let data = if compressed.len() < wav.len() { compressed } else { wav };
        let limit = (limit_mb.clamp(1, MAX_LIMIT_MB) as u64) << 20;
        let name = format!(
            "{}-{}_p{}.wav",
            jiff::Timestamp::now().strftime("%Y%m%d-%H%M%S"),
            rand_hex(5),
            (prob.clamp(0.0, 1.0) * 100.0) as u32
        );
        let path = self.path(CLIPS, kind, &name)?;
        fs::create_dir_all(path.parent().unwrap()).map_err(|e| e.to_string())?;
        if cfg!(unix) && crate::plugins::install::free_mb(&self.root) < 4 {
            return Err("на колонке осталось меньше 4 МБ".into());
        }
        let mut clips = self.list_dir(CLIPS);
        let mut used = clips.iter().map(|clip| clip.size).sum::<u64>();
        clips.sort_by_key(|clip| {
            (
                clip.reviewed,
                !matches!(clip.kind.as_str(), "bg" | "near"),
                clip.time.clone(),
            )
        });
        for clip in clips {
            if used + data.len() as u64 <= limit {
                break;
            }
            if clip.reviewed {
                return Ok(());
            }
            if fs::remove_file(self.path(CLIPS, &clip.kind, &clip.name)?).is_ok() {
                used -= clip.size;
            }
        }
        if used + data.len() as u64 > limit {
            return Ok(());
        }
        write_atomic(&path, &data, 0o600).map_err(|e| e.to_string())
    }

    pub fn read(&self, kind: &str, name: &str) -> Result<Vec<u8>, String> {
        let _lock = self.files.lock().unwrap();
        let (path, _) = self.locate(kind, name)?;
        read_clip(&path)
    }

    pub fn move_clip(&self, kind: &str, name: &str, to: &str) -> Result<(), String> {
        let _lock = self.files.lock().unwrap();
        let (source, storage) = self.locate(kind, name)?;
        if to == "delete" {
            return fs::remove_file(source).map_err(|e| e.to_string());
        }
        let stem = name.trim_end_matches(".wav").trim_end_matches("_r");
        let mut target = self.path(storage, to, &format!("{stem}_r.wav"))?;
        if target == source {
            return Ok(());
        }
        if target.exists() {
            target = self.path(storage, to, &format!("{stem}_{}_r.wav", rand_hex(4)))?;
        }
        fs::create_dir_all(target.parent().unwrap()).map_err(|e| e.to_string())?;
        fs::rename(source, target).map_err(|e| e.to_string())
    }

    pub fn clear(&self) -> Result<(), String> {
        let _lock = self.files.lock().unwrap();
        for clip in self.list() {
            fs::remove_file(self.path(clip.storage, &clip.kind, &clip.name)?).map_err(|e| e.to_string())?;
        }
        Ok(())
    }

    /// Compact the internal files without changing their logical names or WAV downloads.
    pub fn compact(&self) -> Result<Value, String> {
        let clips = {
            let _lock = self.files.lock().unwrap();
            self.list()
        };
        let mut changed = 0;
        let mut saved = 0;
        for clip in clips {
            let _lock = self.files.lock().unwrap();
            if self.stopped.load(Ordering::Acquire) { break; }
            let path = self.path(clip.storage, &clip.kind, &clip.name)?;
            let Ok(meta) = path.symlink_metadata() else { continue };
            if !meta.is_file() || meta.len() > MAX_CLIP { continue; }
            let bytes = fs::read(&path).map_err(|e| e.to_string())?;
            if bytes.starts_with(GZIP) || !bytes.starts_with(b"RIFF") { continue; }
            let smaller = compress(&bytes).map_err(|e| e.to_string())?;
            if smaller.len() >= bytes.len() { continue; }
            write_atomic(&path, &smaller, 0o600).map_err(|e| e.to_string())?;
            if let (Ok(file), Ok(time)) = (fs::File::open(&path), meta.modified()) {
                let _ = file.set_modified(time);
            }
            changed += 1;
            saved += bytes.len() - smaller.len();
        }
        Ok(json!({"changed":changed,"saved_bytes":saved}))
    }

    pub fn archive(&self, data_dir: &Path, sensitivity: i64) -> Result<Vec<u8>, String> {
        let _lock = self.files.lock().unwrap();
        let clips = self.list();
        if clips.iter().map(|c| c.size).sum::<u64>() > MAX_ARCHIVE {
            return Err("Архив больше 32 МБ. Скачайте записи по одной".into());
        }
        let mut zip = zip::ZipWriter::new(Cursor::new(Vec::new()));
        let options = archive_options();
        let mut add = |name: &str, data: &[u8]| -> zip::result::ZipResult<()> {
            zip.start_file(name, options)?;
            Ok(zip.write_all(data)?)
        };
        let model = fs::read(data_dir.join(crate::wake_profiles::TUNED_MODEL))
            .or_else(|_| fs::read(data_dir.join("wake.bin")))
            .map_err(|e| e.to_string())?;
        add("wake.bin", &model).map_err(|e| e.to_string())?;
        add(
            "wake.json",
            json!({"sensitivity":sensitivity}).to_string().as_bytes(),
        )
        .map_err(|e| e.to_string())?;
        add(
            "finetune.py",
            include_bytes!("../../modules/wake-train/assets/finetune.py"),
        )
        .map_err(|e| e.to_string())?;
        let mut total = 0;
        for clip in clips {
            let data = read_clip(&self.path(clip.storage, &clip.kind, &clip.name)?)?;
            total += data.len() as u64;
            if total > MAX_ARCHIVE {
                return Err("Архив больше 32 МБ. Скачайте записи по одной".into());
            }
            add(&format!("{}/{}", clip.kind, clip.name), &data).map_err(|e| e.to_string())?;
        }
        zip.finish()
            .map(|c| c.into_inner())
            .map_err(|e| e.to_string())
    }
}

fn valid_name(name: &str) -> bool {
    name.len() <= 112
        && name.ends_with(".wav")
        && name.as_bytes()[0].is_ascii_alphanumeric()
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
}

fn read_clip(path: &Path) -> Result<Vec<u8>, String> {
    if !path
        .symlink_metadata()
        .is_ok_and(|m| m.is_file() && m.len() <= MAX_CLIP)
    {
        return Err("Запись не найдена".into());
    }
    let mut bytes = Vec::new();
    fs::File::open(path)
        .and_then(|f| f.take(MAX_CLIP + 1).read_to_end(&mut bytes))
        .map_err(|e| e.to_string())?;
    if bytes.len() as u64 > MAX_CLIP {
        return Err("Запись слишком большая".into());
    }
    if !bytes.starts_with(GZIP) { return Ok(bytes); }
    let mut wav = Vec::new();
    GzDecoder::new(bytes.as_slice())
        .take(MAX_CLIP + 1)
        .read_to_end(&mut wav)
        .map_err(|e| e.to_string())?;
    if wav.len() as u64 > MAX_CLIP || !wav.starts_with(b"RIFF") {
        return Err("Повреждена сжатая запись".into());
    }
    Ok(wav)
}

fn compress(bytes: &[u8]) -> std::io::Result<Vec<u8>> {
    let mut encoder = GzEncoder::new(Vec::new(), Compression::fast());
    encoder.write_all(bytes)?;
    encoder.finish()
}

pub fn pcm_wav(pcm: &[i16]) -> Vec<u8> {
    let size = (pcm.len() * 2) as u32;
    let mut bytes = Vec::with_capacity(44 + size as usize);
    bytes.extend_from_slice(b"RIFF");
    bytes.extend_from_slice(&(36 + size).to_le_bytes());
    bytes.extend_from_slice(b"WAVEfmt \x10\0\0\0\x01\0\x01\0");
    bytes.extend_from_slice(&(RATE as u32).to_le_bytes());
    bytes.extend_from_slice(&((RATE * 2) as u32).to_le_bytes());
    bytes.extend_from_slice(b"\x02\0\x10\0data");
    bytes.extend_from_slice(&size.to_le_bytes());
    bytes.extend(pcm.iter().flat_map(|sample| sample.to_le_bytes()));
    bytes
}

fn archive_options() -> zip::write::SimpleFileOptions {
    zip::write::SimpleFileOptions::default()
        .compression_method(zip::CompressionMethod::Deflated)
        .compression_level(Some(3))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exports_existing_and_new_recordings_with_labels_and_model() {
        let root = std::env::temp_dir().join(format!("jarvis-dataset-{}", rand_hex(5)));
        let records = Records::new(root.clone());
        let old = root.join(OLD_CLIPS).join("near").join("legacy.wav");
        fs::create_dir_all(old.parent().unwrap()).unwrap();
        fs::write(&old, vec![0; 1 << 20]).unwrap();
        let pcm: Vec<_> = (0..16000).map(|i| (i % 100) as i16).collect();
        records.save("near", &pcm, 0.49, 1).unwrap();
        assert!(old.is_file());
        let clip = records.list_dir(CLIPS).pop().unwrap();
        assert!(!clip.reviewed);
        assert!(records.read("near", "../wake.bin").is_err());
        records.move_clip("near", &clip.name, "pos").unwrap();
        let reviewed = records.list_dir(CLIPS).pop().unwrap();
        assert!(reviewed.reviewed);
        assert_eq!(&records.read("pos", &reviewed.name).unwrap()[..4], b"RIFF");
        fs::write(root.join("wake.bin"), b"model").unwrap();
        let data = records.archive(&root, 8).unwrap();
        let mut zip = zip::ZipArchive::new(Cursor::new(data)).unwrap();
        assert!(zip.by_name("finetune.py").is_ok());
        assert!(zip.by_name("near/legacy.wav").is_ok());
        assert!(zip.by_name(&format!("pos/{}", reviewed.name)).is_ok());
        assert!(zip.by_name("wake.bin").is_ok());
        let raw = pcm_wav(&pcm);
        fs::write(&old, &raw).unwrap();
        assert_eq!(records.compact().unwrap()["changed"], 1);
        assert_eq!(records.compact().unwrap()["changed"], 0);
        assert_eq!(records.read("near", "legacy.wav").unwrap(), raw);
        fs::remove_dir_all(root).unwrap();
    }
}
