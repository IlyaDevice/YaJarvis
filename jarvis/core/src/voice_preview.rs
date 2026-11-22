use super::{
    Engine, INTERRUPTED, Journal, Mode, Profile, Session, Settings, chatgpt, peak,
};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{
    fs::{self, File},
    io::Read,
    path::Path,
    sync::{
        Arc, Mutex,
        atomic::{AtomicU64, Ordering},
        mpsc::RecvTimeoutError,
    },
    thread,
    time::{Duration, Instant},
};

const PHRASE: &str = "Привет! Я Джарвис. Так звучит мой голос.";
const INSTRUCTIONS: &str =
    "Ты озвучиваешь пробную фразу. Произнеси её дословно, ничего не добавляй.";
const MAGIC: &[u8; 4] = b"YVP1";
const HEADER: usize = 12;
const MAX_SECONDS: usize = 10;
const MAX_FILE: usize = HEADER + 2 * 48_000 * MAX_SECONDS;
const CACHE_BUDGET: u64 = 4 << 20;

#[derive(Default)]
pub(super) struct Previews {
    generation: AtomicU64,
    player: Mutex<()>,
}

#[derive(Deserialize)]
pub(crate) struct Request {
    #[serde(default)]
    mode: String,
    engine: String,
    #[serde(default)]
    tts_engine: String,
    voice: String,
    #[serde(default)]
    tts_model: String,
    #[serde(default)]
    live_model: String,
}

impl Request {
    fn profile(self) -> Result<Profile, String> {
        let model_ok = |s: &str| {
            s.len() <= 160 && !s.chars().any(|c| c.is_control() || c.is_whitespace())
        };
        if !model_ok(&self.tts_model) || !model_ok(&self.live_model) {
            return Err("Некорректный голос или модель".into());
        }
        let is_classic = self.mode == "classic"
            || self.engine == "classic"
            || self.engine == "elevenlabs"
            || !self.tts_engine.is_empty();
        if is_classic {
            let tts_engine = if !self.tts_engine.is_empty() {
                self.tts_engine
            } else if self.engine == "openai" {
                "openai".into()
            } else {
                "elevenlabs".into()
            };
            if !crate::plugins::valid_id(&tts_engine) {
                return Err("Неверный идентификатор плагина озвучки".into());
            }
            let builtin = matches!(tts_engine.as_str(), "openai" | "elevenlabs");
            let voice_ok = self.voice.len() <= 128
                && if builtin {
                    !self.voice.is_empty()
                        && self
                            .voice
                            .bytes()
                            .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b))
                } else {
                    !self.voice.chars().any(|c| c.is_control() || c.is_whitespace())
                };
            if !voice_ok {
                return Err("Некорректный голос или модель".into());
            }
            return Ok(Profile {
                mode: "classic".into(),
                engine: "classic".into(),
                tts_engine,
                tts_voice: self.voice.clone(),
                tts_model: self.tts_model,
                live_model: self.live_model,
                web_search: false,
                voice: self.voice,
                ..Default::default()
            });
        }
        let engine = match self.engine.as_str() {
            "live" => "openai".into(),
            "openai" | "chatgpt" | "gemini" | "grok" | "eleven_realtime" => self.engine,
            _ => return Err("Выберите голосовой движок".into()),
        };
        let id = |s: &str| {
            s.len() <= 128
                && s.bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b))
        };
        if self.voice.is_empty() || !id(&self.voice) {
            return Err("Некорректный голос или модель".into());
        }
        let voice = if engine == "chatgpt" {
            chatgpt::voice(&self.voice).ok_or("Этот голос недоступен для подписки ChatGPT")?
        } else {
            self.voice
        };
        Ok(Profile {
            mode: "live".into(),
            tts_voice: voice.clone(),
            tts_model: self.tts_model,
            live_model: self.live_model,
            web_search: false,
            engine,
            voice,
            ..Default::default()
        })
    }
}

pub(super) struct Sample {
    pub rate: usize,
    pub boost: f64,
    pub pcm: Vec<i16>,
}

impl Sample {
    pub(super) fn encode(&self) -> Vec<u8> {
        let mut bytes = Vec::with_capacity(HEADER + self.pcm.len() * 2);
        bytes.extend_from_slice(MAGIC);
        bytes.extend_from_slice(&(self.rate as u32).to_le_bytes());
        bytes.extend_from_slice(&(self.boost as f32).to_le_bytes());
        for value in &self.pcm {
            bytes.extend_from_slice(&value.to_le_bytes());
        }
        bytes
    }

    fn decode(bytes: &[u8]) -> Option<Self> {
        if bytes.len() <= HEADER
            || bytes.len() > MAX_FILE
            || bytes.len() % 2 != 0
            || &bytes[..4] != MAGIC
        {
            return None;
        }
        let rate = u32::from_le_bytes(bytes[4..8].try_into().ok()?) as usize;
        let boost = f32::from_le_bytes(bytes[8..12].try_into().ok()?) as f64;
        let pcm = &bytes[HEADER..];
        if !matches!(rate, 16_000 | 24_000 | 48_000)
            || !(0.0..=16.0).contains(&boost)
            || pcm.len() / 2 > rate * MAX_SECONDS
        {
            return None;
        }
        Some(Self {
            rate,
            boost,
            pcm: pcm
                .chunks_exact(2)
                .map(|b| i16::from_le_bytes([b[0], b[1]]))
                .collect(),
        })
    }

    pub(super) fn read(path: &Path) -> Option<Self> {
        let mut bytes = Vec::new();
        File::open(path)
            .ok()?
            .take((MAX_FILE + 1) as u64)
            .read_to_end(&mut bytes)
            .ok()?;
        Self::decode(&bytes)
    }
}

impl Engine {
    /// Returns true when the sample was played from disk.
    pub(crate) async fn preview(self: Arc<Self>, request: Request) -> Result<bool, String> {
        let profile = request.profile()?;
        let seq = self.previews.generation.fetch_add(1, Ordering::SeqCst) + 1;
        let epoch = self.speech_epoch.load(Ordering::SeqCst);
        tokio::task::spawn_blocking(move || self.preview_blocking(profile, seq, epoch))
            .await
            .map_err(|_| "Проверка голоса прервана".to_string())?
    }

    fn preview_blocking(&self, profile: Profile, seq: u64, epoch: u64) -> Result<bool, String> {
        let _serial = self.previews.player.lock().unwrap();
        let cancelled = || {
            self.previews.generation.load(Ordering::SeqCst) != seq
                || self.speech_epoch.load(Ordering::SeqCst) != epoch
        };
        if cancelled() {
            return Err(INTERRUPTED.into());
        }
        let _speaker = {
            let mut st = self.st.lock().unwrap();
            if st.busy
                || st.live.is_some()
                || self.player.playing()
                || self
                    .saying
                    .compare_exchange(false, true, Ordering::SeqCst, Ordering::Relaxed)
                    .is_err()
            {
                return Err(
                    "Джарвис сейчас занят. Остановите разговор и попробуйте ещё раз.".into(),
                );
            }
            st.busy = true;
            PreviewSpeaker(self)
        };

        let directory = self.data.join("voice-samples");
        let path = directory.join(cache_name(&profile));
        let cached = Sample::read(&path);
        let from_cache = cached.is_some();
        let sample = match cached {
            Some(sample) => sample,
            None => {
                let settings = self.store.get();
                let missing = crate::engine::tts_missing(&self.host, &settings, &profile);
                if !missing.is_empty() {
                    return Err(missing);
                }
                let session = self.preview_session(settings, profile);
                let result = capture(&*session, &cancelled);
                session.close();
                result?
            }
        };
        self.play_preview(&sample, &cancelled)?;
        if !from_cache {
            if let Err(error) = fs::create_dir_all(&directory)
                .and_then(|()| crate::settings::write_atomic(&path, &sample.encode(), 0o600))
            {
                log::warn!("Кеш пробы голоса: {error}");
            } else {
                prune(&directory, &path, CACHE_BUDGET);
            }
        }
        Ok(from_cache)
    }

    pub(super) fn preview_session(&self, settings: Settings, profile: Profile) -> Arc<dyn Session> {
        let journal = Arc::new(Journal::default());
        if profile.is_classic() {
            return crate::classic_voice::Classic::open(
                &self.rt,
                self.host.clone(),
                self.tools.clone(),
                journal,
                settings,
                profile,
                INSTRUCTIONS.into(),
                "Джарвис".into(),
            );
        }
        match profile.engine.as_str() {
            "chatgpt" => chatgpt::ChatGptSession::open(
                &self.rt,
                chatgpt::Open {
                    host: self.host.clone(),
                    voice: profile.voice,
                    thread: serde_json::Value::Null,
                    instructions: INSTRUCTIONS.into(),
                    brain: None,
                    journal,
                    memory: None,
                    mic_boost_db: 0,
                },
            ),
            _ => crate::cloud_voice::Voice::open(
                &self.rt,
                self.host.clone(),
                None,
                journal,
                settings,
                profile,
                INSTRUCTIONS.into(),
            ),
        }
    }

    fn play_preview(&self, sample: &Sample, cancelled: &impl Fn() -> bool) -> Result<(), String> {
        if cancelled() {
            return Err(INTERRUPTED.into());
        }
        self.player
            .start(sample.rate)
            .map_err(|error| error.to_string())?;
        self.player.set_boost(sample.boost);
        self.leds.set(Mode::Speaking);
        let start = Instant::now();
        let mut sent = 0;
        for chunk in sample.pcm.chunks(sample.rate / 25) {
            if cancelled() {
                return Err(INTERRUPTED.into());
            }
            self.player.write(chunk).map_err(str::to_string)?;
            self.leds.level(peak(chunk) / 9000.0);
            sent += chunk.len();
            let elapsed = Duration::from_secs_f64(sent as f64 / sample.rate as f64);
            thread::sleep(elapsed.saturating_sub(start.elapsed()));
        }
        if cancelled() {
            return Err(INTERRUPTED.into());
        }
        self.player.finish(Duration::from_secs(2));
        Ok(())
    }
}

struct PreviewSpeaker<'a>(&'a Engine);

impl Drop for PreviewSpeaker<'_> {
    fn drop(&mut self) {
        self.0.player.stop(true);
        self.0.leds.set(Mode::Off);
        let mut state = self.0.st.lock().unwrap();
        state.busy = false;
        state.aborted = false;
        self.0.saying.store(false, Ordering::SeqCst);
    }
}

fn capture(session: &dyn Session, cancelled: &impl Fn() -> bool) -> Result<Sample, String> {
    let started = Instant::now();
    let ready = session.ready(Duration::from_secs(10));
    if cancelled() {
        return Err(INTERRUPTED.into());
    }
    ready?;
    let output = session
        .take_audio()
        .ok_or("Нет звука от голосового движка")?;
    capture_phrase(
        session,
        &output,
        PHRASE,
        Duration::from_secs(25).saturating_sub(started.elapsed()),
        cancelled,
    )
}

pub(super) fn capture_phrase(
    session: &dyn Session,
    output: &std::sync::mpsc::Receiver<Vec<i16>>,
    text: &str,
    limit: Duration,
    cancelled: &impl Fn() -> bool,
) -> Result<Sample, String> {
    let started = Instant::now();
    if cancelled() {
        return Err(INTERRUPTED.into());
    }
    session.say(text)?;
    let mut sample = Sample {
        rate: session.rate(),
        boost: session.out_boost(),
        pcm: Vec::new(),
    };
    let mut last_audio: Option<(Instant, usize)> = None;
    loop {
        if cancelled() {
            return Err(INTERRUPTED.into());
        }
        if started.elapsed() > limit {
            return Err("Голосовой движок не успел произнести образец".into());
        }
        if last_audio.is_some_and(|(at, _)| at.elapsed() >= Duration::from_millis(900)) {
            break;
        }
        let pcm = match output.recv_timeout(Duration::from_millis(80)) {
            Ok(pcm) => pcm,
            Err(RecvTimeoutError::Timeout) => continue,
            Err(RecvTimeoutError::Disconnected) => break,
        };
        // Ignore initial silence. Keep internal pauses and a short tail.
        if peak(&pcm) > 100.0 {
            last_audio = Some((Instant::now(), sample.pcm.len() + pcm.len()));
        }
        if last_audio.is_some() {
            if sample.pcm.len() + pcm.len() > sample.rate * MAX_SECONDS {
                return Err("Проба голоса длиннее 10 секунд".into());
            }
            sample.pcm.extend(pcm);
        }
    }
    let (_, end) = last_audio.ok_or("Образец голоса не прозвучал")?;
    sample.pcm.truncate(end + sample.rate / 8);
    Ok(sample)
}

fn cache_name(profile: &Profile) -> String {
    sample_name(profile, PHRASE)
}

pub(super) fn sample_name(profile: &Profile, phrase: &str) -> String {
    let (engine, voice): (&str, &str) = if profile.is_classic() {
        (
            profile.effective_tts_engine(),
            if !profile.tts_voice.is_empty() {
                &profile.tts_voice
            } else {
                &profile.voice
            },
        )
    } else {
        (
            &profile.engine,
            if profile.engine == "eleven_realtime" && !profile.tts_voice.is_empty() {
                &profile.tts_voice
            } else {
                &profile.voice
            },
        )
    };
    let live_model = if engine == "chatgpt" {
        "gpt-live-1.1-codex"
    } else {
        &profile.live_model
    };
    let identity = (
        engine,
        voice,
        &profile.tts_model,
        live_model,
        crate::version(),
        phrase,
    );
    let data = serde_json::to_vec(&identity).expect("voice sample key");
    format!("{}.pcm", hex::encode(Sha256::digest(data)))
}

pub(super) fn prune(directory: &Path, newest: &Path, budget: u64) {
    let Ok(entries) = fs::read_dir(directory) else {
        return;
    };
    let mut files: Vec<_> = entries
        .filter_map(Result::ok)
        .filter_map(|entry| {
            let path = entry.path();
            let info = entry.metadata().ok()?;
            if !info.is_file() || path.extension()? != "pcm" {
                return None;
            }
            Some((path == newest, info.modified().ok()?, info.len(), path))
        })
        .collect();
    files.sort_unstable();
    let mut kept = 0;
    for (_, _, size, path) in files.into_iter().rev() {
        if kept + size <= budget {
            kept += size;
        } else if fs::remove_file(path).is_err() {
            kept += size;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc::{Receiver, channel};

    struct Voice {
        audio: Mutex<Option<Receiver<Vec<i16>>>>,
        rate: usize,
        dialog: crate::session::Dialog,
    }

    impl Session for Voice {
        fn feed(&self, _: &[i16]) {
            panic!("a voice preview must never send microphone audio");
        }
        fn take_audio(&self) -> Option<Receiver<Vec<i16>>> {
            self.audio.lock().unwrap().take()
        }
        fn rate(&self) -> usize {
            self.rate
        }
        fn ready(&self, _: Duration) -> Result<(), String> {
            Ok(())
        }
        fn say(&self, text: &str) -> Result<(), String> {
            assert_eq!(text, PHRASE);
            Ok(())
        }
        fn close(&self) {}
        fn dialog(&self) -> &crate::session::Dialog {
            &self.dialog
        }
        fn out_boost(&self) -> f64 {
            1.5
        }
    }

    #[test]
    fn cached_audio_retains_duration_and_rate_for_all_engines() {
        for rate in [16_000, 24_000, 48_000] {
            let (send, audio) = channel();
            send.send(vec![1500; rate * 2]).unwrap();
            drop(send);
            let voice = Voice {
                audio: Mutex::new(Some(audio)),
                rate,
                dialog: crate::session::Dialog::new(Arc::new(Journal::default()), "test", "test"),
            };
            let original = capture(&voice, &|| false).unwrap();
            let hit = Sample::decode(&original.encode()).unwrap();
            assert_eq!(hit.rate, rate);
            assert_eq!(hit.pcm.len() / hit.rate, 2);
            assert_eq!(hit.pcm, original.pcm);
            assert_eq!(hit.boost, original.boost);
        }
        // The Go cache was raw PCM without a sample rate.
        assert!(Sample::decode(&vec![0; 48_000 * 2]).is_none());
    }
}
