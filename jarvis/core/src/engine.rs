//! Главный цикл: микрофон → слово «Джарвис» → разговор с моделью → динамик. Алиса имеет приоритет:
//! позвали её — Джарвис сразу замолкает и закрывает разговор.

use crate::{
    alice,
    audio::{Mic, Player},
    chatgpt, fastcmd,
    journal::Journal,
    led::{Leds, Mode},
    plugins::Host,
    rules,
    session::Session,
    settings::{Profile, Settings, Store},
    tools::Tools,
    wake::Listener,
};
use jarvis_dsp::Wake;
use std::{
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, Ordering},
        mpsc::RecvTimeoutError,
    },
    time::{Duration, Instant},
};

const RATE: usize = 16000;
const INTERRUPTED: &str = "озвучку прервали";
#[path="text.rs"]
mod text;

#[derive(Default)]
struct St {
    live: Option<Arc<dyn Session>>,
    busy: bool,
    holding: bool,   // разговора с моделью ещё нет — звук с момента слова копится в hold
    hold: Vec<i16>,  // уйдёт модели целиком, когда разговор откроется
    aborted: bool,   // «Стоп» или «Алиса», пока разговор ещё не открыт
    ring: Vec<i16>,  // последние ~3 с микрофона: само слово и начало фразы
    last_wake: Option<Instant>,
    last_loud: Option<Instant>,
    pending_wake: bool,
}

pub struct Engine {
    pub store: Store,
    pub host: Arc<Host>,
    pub mic: Arc<Mic>,
    pub player: Arc<Player>,
    pub leds: Arc<Leds>,
    pub journal: Arc<Journal>,
    pub tools: Arc<Tools>,
    pub music: Arc<crate::music::Music>,
    pub listener: Arc<Listener>,
    pub data: PathBuf,
    rt: tokio::runtime::Handle,
    st: Mutex<St>,
    reset_wake: AtomicBool,
    recording_word: AtomicBool,
    training_until: Mutex<Option<Instant>>,
    speech_epoch: AtomicU64,
    saying: AtomicBool,
    cmds: std::sync::OnceLock<crate::cmdspot::CmdSpotter>,
}

fn same(a: &Arc<dyn Session>, b: &Arc<dyn Session>) -> bool {
    std::ptr::addr_eq(Arc::as_ptr(a), Arc::as_ptr(b))
}

pub fn peak(pcm: &[i16]) -> f64 {
    pcm.iter().map(|v| v.unsigned_abs()).max().unwrap_or(0) as f64
}

pub fn loud(pcm: &[i16]) -> bool {
    pcm.iter().any(|&v| !(-600..=600).contains(&v))
}

/// Копия звука с усилением db (с ограничением по краям шкалы).
pub fn boost(pcm: &[i16], db: i64) -> Vec<i16> {
    if db <= 0 {
        return pcm.to_vec();
    }
    let k = 10f64.powf(db as f64 / 20.0);
    pcm.iter().map(|&v| (v as f64 * k).clamp(-32767.0, 32767.0) as i16).collect()
}

fn short(e: &str) -> String {
    if e.chars().count() > 160 { format!("{}…", e.chars().take(160).collect::<String>()) } else { e.into() }
}

fn lang_name(l: &str) -> &'static str {
    match l {
        "ru" => "русском",
        "en" => "английском",
        "uk" => "украинском",
        _ => "",
    }
}

/// Чего не хватает, чтобы говорить выбранным движком ("" — всё на месте).
pub fn key_missing(host: &Host, s: &Settings, engine: &str) -> String {
    let id = match engine {
        "" | "live" => "openai",
        "eleven_realtime" => "elevenlabs",
        id => id,
    };
    if host.provider(id).is_err() {
        return format!("Установите и включите плагин «{id}» в «Подключениях»");
    }
    match engine {
        "chatgpt" if !chatgpt::signed_in(&s.chatgpt) => "не выполнен вход в ChatGPT — войдите в «Подключениях»".into(),
        "chatgpt" => chatgpt::voice_limit().unwrap_or_default(),
        "" | "live" | "openai" => if s.openai_key.is_empty(){"Добавьте ключ OpenAI в «Подключениях»".into()}else{String::new()},
        "gemini" => if s.gemini_key.is_empty(){"Добавьте ключ Gemini в «Подключениях»".into()}else{String::new()},
        "grok" => if s.grok_key.is_empty(){"Добавьте ключ Grok в «Подключениях»".into()}else{String::new()},
        "elevenlabs"|"eleven_realtime"=>if s.elevenlabs_key.is_empty(){"Добавьте ключ ElevenLabs в «Подключениях»".into()}else{String::new()},
        other => format!("движок «{other}» ещё не перенесён в новое ядро — выберите ассистента на ChatGPT"),
    }
}

pub struct WakeRecording(Arc<Engine>);

impl Drop for WakeRecording {
    fn drop(&mut self) {
        self.0.reset_wake.store(true, Ordering::Release);
        self.0.recording_word.store(false, Ordering::Release);
    }
}

impl Engine {
    pub fn recording_word(self: &Arc<Self>) -> WakeRecording {
        self.recording_word.store(true, Ordering::Release);
        WakeRecording(self.clone())
    }

    pub fn set_background_training(&self, minutes: Option<u32>) {
        *self.training_until.lock().unwrap() = minutes.map(|mins| Instant::now() + Duration::from_secs(u64::from(mins) * 60 + 1));
    }
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        rt: tokio::runtime::Handle,
        store: Store,
        host: Arc<Host>,
        mic: Arc<Mic>,
        player: Arc<Player>,
        leds: Arc<Leds>,
        journal: Arc<Journal>,
        tools: Arc<Tools>,
        listener: Arc<Listener>,
        data: PathBuf,
        music: Arc<crate::music::Music>,
    ) -> Arc<Engine> {
        Arc::new(Engine { store, host, mic, player, leds, journal, tools, music, listener, data, rt, st: Mutex::default(), reset_wake: AtomicBool::new(false),
            recording_word: AtomicBool::new(false), training_until: Mutex::new(None),
            speech_epoch: AtomicU64::new(0), saying: AtomicBool::new(false), cmds: Default::default() })
    }

    /// Слушать микрофон в своём потоке: слово, звук в разговор, перебивание.
    pub fn run(self: &Arc<Self>, mut det: Box<dyn Wake>) {
        let w = Arc::downgrade(self);
        if let Some(c) = crate::cmdspot::CmdSpotter::new(
            &self.data,
            Arc::new(move |p: String| {
                if let Some(e) = w.upgrade() {
                    e.on_command(&p);
                }
            }),
        ) {
            let _ = self.cmds.set(c);
        }
        let e = self.clone();
        let training = crate::training_io::AudioBatch::channel(&self.rt, self.host.clone());
        std::thread::Builder::new()
            .name("engine".into())
            .spawn(move || {
                let (_, rx) = e.mic.subscribe(256);
                let mut batch = crate::training_io::AudioBatch::default();
                for raw in rx {
                    let training_enabled = e.host.wake_training();
                    if let Some(replacement)=e.listener.take_reload(&e.data,&e.store,training_enabled){det=replacement;}
                    let (live, busy) = {
                        let mut st = e.st.lock().unwrap();
                        // последние 3 с; буфер растёт до 6 с и сдвигается на месте — без лишних копий
                        if st.ring.len() + raw.len() > 6 * RATE {
                            let from = st.ring.len().saturating_sub(3 * RATE);
                            st.ring.drain(..from);
                        }
                        st.ring.extend_from_slice(&raw);
                        if loud(&raw) {
                            st.last_loud = Some(Instant::now());
                        }
                        if st.holding && st.live.is_none() && st.hold.len() < 20 * RATE {
                            st.hold.extend_from_slice(&raw);
                        }
                        (st.live.clone(), st.busy)
                    };
                    if busy || live.is_some() {
                        e.leds.hear(peak(&raw) / 9000.0);
                    }
                    if let Some(c) = e.cmds.get() {
                        if busy || live.is_some() || e.music_playing() {
                            c.feed(&raw);
                        } else {
                            c.idle();
                        }
                    }
                    if e.reset_wake.swap(false, Ordering::Relaxed) {
                        det.reset();
                    }
                    let (enabled, boost_db) = e.store.with(|s| (s.wake_enabled, s.mic_boost_db));
                    let ready = e.listener.ready.load(Ordering::Relaxed);
                    let enabled = enabled && !e.recording_word.load(Ordering::Acquire);
                    let hit = ready && enabled && det.feed(&raw); // поток признаков непрерывный, и во время разговора
                    let prob = det.last_prob();
                    e.listener.last_prob.store(prob.to_bits(), Ordering::Relaxed);
                    if training_enabled {
                        let (collect, limit) = e.store.with(|s| (s.train_mode, s.train_limit_mb));
                        let collect = collect && e.listener.phrase() == crate::wake_profiles::DEFAULT_PHRASE;
                        let background = e.training_until.lock().unwrap().is_some_and(|until| until > Instant::now());
                        if collect || background {
                            let discard = busy || live.is_some() || e.saying.load(Ordering::Relaxed) || e.player.playing() || !ready || !enabled;
                            if let Some(frame) = batch.push(&raw, hit, discard, prob, collect, limit) {
                                let _ = training.try_send(frame);
                            }
                        } else {
                            batch.reset();
                        }
                    } else {
                        batch.reset();
                    }
                    if let Some(live) = live {
                        live.feed(&if boost_db > 0 { boost(&raw, boost_db) } else { raw.to_vec() });
                        // «Джарвис» во время разговора — как у Алисы: сразу замолкает и слушает
                        let recent = e.st.lock().unwrap().last_wake.is_some_and(|t| t.elapsed() < Duration::from_millis(2500));
                        if hit && !recent {
                            e.interrupt(&live);
                        }
                        continue;
                    }
                    if busy {
                        continue;
                    }
                    if hit {
                        let p = det.wake_prob();
                        det.reset(); // хвост этого же слова не должен сработать ещё раз
                        e.trigger("слово", p);
                    }
                }
            })
            .expect("поток движка");
        self.watch_alice();
    }

    /// Системное сообщение колонки: сразу локальный сигнал (без сети и ключей — подтверждение есть всегда),
    /// затем голосом. Озвучка голосом ассистента ещё не перенесена — пока говорит штатная Алиса.
    pub fn system_notice(&self, chime: &[i16], text: &str, alice_text: &str) {
        if self.player.start(RATE).is_ok() {
            let _ = self.player.write(&vec![0; RATE / 4]); // тракт просыпается
            let _ = self.player.write(chime);
            self.player.finish(Duration::from_secs(4));
        }
        log::info!("системное сообщение: {text}");
        if !alice_text.is_empty()
            && let Err(e) = alice::text(alice_text, false)
        {
            log::info!("системное сообщение: Алиса недоступна: {e}");
        }
    }

    /// Состояние из событий музыкального модуля: без запросов на каждом кадре микрофона.
    fn music_playing(&self) -> bool {
        self.music.playing()
    }

    /// Встроенный распознаватель услышал команду, пока Джарвис занят.
    fn on_command(&self, phrase: &str) {
        let w = phrase.to_lowercase();
        let has = |s: &str| w.contains(s);
        if !self.talking() {
            if !self.music_playing() { return; }
            if has("стоп") || has("пауза") {
                let op = if has("пауза") { "pause" } else { "stop" };
                let _ = self.rt.block_on(self.music.transport(&self.host, op, serde_json::Value::Null));
                return;
            }
            if has("громче") || has("тише") {
                let level = crate::speakervol::speaker_volume().unwrap_or(5) + if has("тише") { -1 } else { 1 };
                let _ = crate::speakervol::set_alice_volume(level.clamp(0,10));
            }
            return;
        }
        if has("хватит") {
            log::info!("Джарвис: «{phrase}» — разговор закрыт, музыка не тронута (встроенный распознаватель)");
            self.end_chat(&format!("сказали «{phrase}»"));
            self.journal.note("стоп", phrase, "");
        } else if has("стоп") || has("пауза") {
            log::info!("Джарвис: «{phrase}» — стоп (встроенный распознаватель)");
            self.stop(&format!("сказали «{phrase}»"));
            self.journal.note("стоп", phrase, "");
        } else if has("громче") || has("тише") {
            let step = if has("тише") { -10 } else { 10 };
            let mut v = 0;
            // громкость голоса активного ассистента
            let _ = self.store.update(|s| -> Result<(), std::io::Error> {
                let id = s.active_profile().id.clone();
                if let Some(p) = s.profiles.iter_mut().find(|p| p.id == id) {
                    p.volume = (p.volume + step).clamp(10, 100);
                    v = p.volume;
                }
                Ok(())
            });
            log::info!("Джарвис: «{phrase}» — громкость {v}% (встроенный распознаватель)");
        } else {
            log::info!("Джарвис: распознаватель услышал «{phrase}» — не моя команда");
        }
    }

    /// Идёт ли разговор (или он открывается).
    pub fn talking(&self) -> bool {
        let st = self.st.lock().unwrap();
        st.busy || st.live.is_some() || self.saying.load(Ordering::Relaxed)
    }

    /// Начать разговор (по слову, по кнопке в настройках).
    pub fn trigger(self: &Arc<Self>, source: &str, prob: f32) {
        if source == "слово" && self.recording_word.load(Ordering::Acquire) { return; }
        let clip = {
            let mut st = self.st.lock().unwrap();
            if st.busy || st.last_wake.is_some_and(|t| t.elapsed() < Duration::from_millis(1500)) {
                return;
            }
            st.busy = true;
            st.last_wake = Some(Instant::now());
            st.aborted = false;
            st.hold.clear();
            st.holding = true;
            st.ring[st.ring.len().saturating_sub(3 * RATE)..].to_vec()
        };
        let by_word = source == "слово";
        // как у Алисы: услышал слово — сразу кольцо и разговор; проверка имени расшифровкой — по галочке
        let sure = !by_word || prob >= 0.9 || !self.store.with(|s| s.wake_check);
        if sure {
            self.leds.set(Mode::Listening);
        }
        self.reset_wake.store(true, Ordering::Relaxed);
        if by_word {
            log::info!("Джарвис: активация ({source}, уверенность {prob:.2})");
        } else {
            log::info!("Джарвис: активация ({source})");
        }
        let e = self.clone();
        std::thread::Builder::new()
            .name("converse".into())
            .spawn(move || {
                let preroll = if by_word { clip.clone() } else { vec![] };
                // сбой внутри разговора не должен оставить Джарвиса «занятым» навсегда
                if std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| e.converse(by_word, sure, preroll, clip))).is_err() {
                    log::info!("Джарвис: разговор оборвался из-за ошибки ядра");
                    e.player.stop(true);
                    if let Some(l) = e.st.lock().unwrap_or_else(|p| p.into_inner()).live.take() {
                        l.close();
                    }
                }
                let queued = {
                    let mut st = e.st.lock().unwrap();
                    let q = st.pending_wake;
                    st.pending_wake = false;
                    st.busy = false;
                    st.live = None;
                    st.aborted = false;
                    st.hold.clear();
                    st.holding = false;
                    q
                };
                e.reset_wake.store(true, Ordering::Relaxed);
                e.leds.set(Mode::Off);
                if queued {
                    e.trigger("слово", 1.0);
                }
            })
            .expect("поток разговора");
    }

    fn instructions(&self, s: &Settings, p: &Profile) -> String {
        let phrase = self.listener.phrase();
        let mut text = s.prompt_text(&p.prompt);
        if p.prompt == "jarvis" || p.prompt == "bro" {
            text = text.replacen("Ты — Джарвис", &format!("Ты — «{phrase}»"), 1);
        }
        let persona = text.clone();
        let n = lang_name(&s.language);
        if !n.is_empty() {
            text += &format!(
                "\n\nХозяин всегда говорит на {n} языке. Понимай его речь как {n}, даже если она звучит неразборчиво или похоже на другой язык, и отвечай только на {n}."
            );
        }
        if !s.city.is_empty() {
            text += &format!("\n\nХозяин живёт в городе {} — погода, время и новости по умолчанию для него.", s.city);
        }
        text += "\n\nКогда для ответа нужен инструмент (поиск в интернете, погода, умный дом, музыка, сложная задача), \
можно ОДИН раз за ответ коротко сказать вслух живую фразу — «Секунду, гляну», «Сейчас поищу» — и сразу вызывать инструмент. \
Если вызываешь несколько инструментов подряд или повторяешь вызов — больше никаких таких фраз: молча дождись результата и скажи итог. \
На простые вопросы и быстрые команды (включить свет, громкость) отвечай сразу, без таких фраз.";
        if s.is_night() {
            text += "\n\nСейчас ночь, в доме могут спать: отвечай коротко и спокойно, без длинных списков.";
        }
        match &self.tools.memory {
            // общая память разговора: у всех ассистентов, переживает перезапуск
            Some(m) => text += &crate::memory::instructions(m, p.engine == "chatgpt"),
            None => {
                let h = self.journal.history();
                if !h.is_empty() {
                    let ctx: Vec<_> = h.iter().map(|t| serde_json::json!({"user": t.text, "jarvis": t.answer})).collect();
                    text += &format!("\n\nПредыдущие реплики разговора: {}", serde_json::Value::Array(ctx));
                }
            }
        }
        if p.engine == "chatgpt" && self.tools.memory.is_some() {
            // напоминание после истории: в длинном чате модель иначе сползает в обычный стиль ChatGPT
            let persona = persona.trim();
            let persona = if persona.chars().count() > 2500 { format!("{}…", persona.chars().take(2500).collect::<String>()) } else { persona.to_owned() };
            text += &format!(
                "\n\nГлавное, не забывай: ты — голосовой ассистент «{phrase}» внутри умной колонки Яндекс Станция у хозяина дома, \
а не ChatGPT в приложении. Говоришь вслух, коротко, своим характером. Что делать на колонке (музыка, YouTube, свет, \
поиск, погода, таймеры, будильники, напоминания) — передаёшь бэкенду, он делает: таймеры и будильники ставит Алиса \
на этой же колонке, так что не говори, что не умеешь. Твой характер:\n{persona}"
            );
        }
        text + rules::SPEAKER_CONTENT_RULES + rules::SPEAKER_WAIT_RULES + &rules::identity(&phrase)
    }

    /// Кто думает и управляет домом, когда голосовая модель передаёт задачу: модель подписки ChatGPT.
    fn brain(&self, p: &Profile) -> chatgpt::Brain {
        let (host, tools, p) = (self.host.clone(), self.tools.clone(), p.clone());
        Arc::new(move |task: String| {
            let (host, tools, p) = (host.clone(), tools.clone(), p.clone());
            Box::pin(async move {
                // вход и подготовка дома параллельно; инструменты зависят от загруженного каталога
                let t0 = Instant::now();
                let ((ctx, defs), auth) = tokio::join!(tools.prepare(), async {
                    host.provider("chatgpt")?.request("auth.token", serde_json::Value::Null, Duration::from_secs(20)).await
                });
                log::info!("бэкенд: контекст и инструменты за {:.2} с", t0.elapsed().as_secs_f64());
                let instr = format!("{}{ctx}", rules::BACKEND_RULES);
                chatgpt::ask_codex(&host, auth?, &p, &instr, &task, Some(&tools), defs).await
            })
        })
    }

    fn chat_thread(&self) -> String {
        if let Some(m) = &self.tools.memory {
            return m.thread();
        }
        std::fs::read(self.data.join("chatgpt-chat.json"))
            .ok()
            .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok())
            .and_then(|v| v.get("id").and_then(|x| x.as_str()).map(String::from))
            .unwrap_or_default()
    }

    /// Открыть разговор с моделью тем движком, который выбран у ассистента.
    fn new_session(&self, s: &Settings, p: &Profile) -> Arc<dyn Session> {
        if p.is_classic(){return crate::classic_voice::Classic::open(&self.rt,self.host.clone(),self.tools.clone(),self.journal.clone(),s.clone(),p.clone(),self.instructions(s,p),self.listener.phrase());}
        if ["","live","openai","gemini","grok","eleven_realtime"].contains(&p.engine.as_str()) {
            return crate::cloud_voice::Voice::open(&self.rt,self.host.clone(),self.tools.clone(),self.journal.clone(),s.clone(),p.clone(),self.instructions(s,p));
        }
        chatgpt::ChatGptSession::open(
            &self.rt,
            chatgpt::Open {
                host: self.host.clone(),
                voice: p.voice.clone(),
                thread: self.chat_thread().into(),
                instructions: self.instructions(s, p),
                brain: Some(self.brain(p)),
                journal: self.journal.clone(),
                memory: self.tools.memory.clone(),
            },
        )
    }

    /// Чем память сжимает старые реплики: текстовая модель подписки ChatGPT (Gemini и OpenAI — когда
    /// появятся их плагины). Выбор — как в Go: выбранный в «Памяти» или первый доступный.
    pub fn summarizer(&self) -> crate::memory::Summarizer {
        let (host, store) = (self.host.clone(), self.store.clone());
        Arc::new(move |instructions: String, text: String| {
            let (host, store) = (host.clone(), store.clone());
            Box::pin(async move {
                let s = store.get();
                let m = &s.memory;
                let chatgpt = chatgpt::signed_in(&s.chatgpt) && host.provider("chatgpt").is_ok();
                if m.off || m.provider == "none" || !chatgpt {
                    return Err(crate::memory::NO_SUMMARIZER.to_string());
                }
                let model = if m.provider == "chatgpt" && crate::settings::BACKEND_MODELS.contains(&m.model.as_str()) {
                    m.model.clone()
                } else {
                    crate::settings::BACKEND_MODELS[0].to_string()
                };
                let p = Profile { backend_model: model, effort: "low".into(), ..Default::default() };
                let auth = host.provider("chatgpt")?.request("auth.token", serde_json::Value::Null, Duration::from_secs(20)).await?;
                chatgpt::ask_codex(&host, auth, &p, &instructions, &text, None, vec![]).await
            })
        })
    }

    fn start_voice(&self, sess: &dyn Session) -> Result<(), String> {
        self.player.start(sess.rate()).map_err(|e| e.to_string())?;
        self.player.set_boost(sess.out_boost());
        Ok(())
    }

    fn earcon(&self, ok: bool) {
        if self.player.start(RATE).is_ok() {
            let _ = self.player.write(&fastcmd::earcon(ok));
            self.player.finish(Duration::from_secs(2));
        }
    }

    /// Один разговор от слова (или кнопки) до тишины.
    fn converse(self: &Arc<Self>, by_word: bool, sure: bool, preroll: Vec<i16>, clip: Vec<i16>) {
        let s = self.store.get();
        let p = s.active_profile();
        let missing = key_missing(&self.host, &s, &p.engine);
        if !missing.is_empty() {
            self.journal.fail(&missing);
            return;
        }
        {
            let t = self.tools.clone();
            self.rt.spawn(async move { t.prewarm().await });
        }
        let idle = if p.idle_s < 2.0 { 8.0 } else { p.idle_s };
        let mut sess: Option<Arc<dyn Session>> = None;
        let mut out = None;
        let mut speaking = false;
        let mut checked = 0i64;
        let mut ready_at = Instant::now();
        let mut last_voice: Option<Instant> = None;
        let _ = (sure, &clip);

        // открыть разговор с моделью и отдать ему весь звук с момента слова
        let open = |sess: &mut Option<Arc<dyn Session>>, out: &mut Option<std::sync::mpsc::Receiver<Vec<i16>>>, ready_at: &mut Instant| -> bool {
            if by_word {
                self.leds.set(Mode::Thinking); // соединяюсь с моделью — бегущий огонёк
            }
            let x = self.new_session(&s, &p);
            {
                let mut st = self.st.lock().unwrap();
                // The wake word has already been detected. Avoid replaying seconds of
                // old audio into the subscription call before the first real question.
                let before = if p.engine == "chatgpt" { preroll.len().saturating_sub(RATE * 5 / 4) } else { 0 };
                let mut first = preroll[before..].to_vec();
                first.extend_from_slice(&st.hold);
                x.feed(&boost(&first, s.mic_boost_db));
                st.hold.clear();
                st.holding = false;
                st.live = Some(x.clone());
            }
            *sess = Some(x.clone());
            if let Err(e) = x.ready(Duration::from_secs(12)) {
                log::info!("разговор не открылся: {e}");
                x.dialog().finish_turn(&format!("разговор не открылся: {}", short(&e)));
                return false;
            }
            if let Err(e) = self.start_voice(&*x) {
                x.dialog().finish_turn(&format!("динамик: {e}"));
                return false;
            }
            *out = x.take_audio();
            *ready_at = Instant::now();
            x.dialog().touch();
            self.leds.set(Mode::Listening);
            true
        };
        let close = |sess: &Option<Arc<dyn Session>>| {
            if let Some(x) = sess {
                x.close();
            }
        };
        if !open(&mut sess, &mut out, &mut ready_at) {
            close(&sess);
            return;
        }
        let x = sess.clone().unwrap();
        let out = out.unwrap();
        let mut tick = Instant::now();
        loop {
            match out.recv_timeout(Duration::from_millis(20)) {
                Err(RecvTimeoutError::Disconnected) => {
                    self.player.stop(true);
                    x.dialog().finish_turn("");
                    return close(&sess);
                }
                Err(RecvTimeoutError::Timeout) => {}
                Ok(pcm) => {
                    if !x.dialog().muted() {
                        if !self.player.playing() && loud(&pcm) {
                            let _ = self.start_voice(&*x); // после перебивания плеер был остановлен
                        }
                        let _ = self.player.write(&pcm);
                        if loud(&pcm) {
                            last_voice = Some(Instant::now());
                        }
                        self.leds.level(peak(&pcm) / 9000.0);
                        if loud(&pcm) != speaking {
                            speaking = !speaking;
                            self.leds.set(if speaking { Mode::Speaking } else { Mode::Listening });
                        }
                    }
                }
            }
            if x.interrupted() {
                while out.try_recv().is_ok(){}
                self.player.stop(true);
                speaking = false;
                self.leds.set(Mode::Listening);
            }
            if tick.elapsed() < Duration::from_millis(100) {
                continue;
            }
            tick = Instant::now();
            let stopped = {
                let st = self.st.lock().unwrap();
                st.aborted || !st.live.as_ref().is_some_and(|l| same(l, &x))
            };
            if stopped {
                self.player.stop(true);
                x.dialog().finish_turn("");
                return close(&sess);
            }
            // разговор открыт сразу по слову, а человек так ничего и не сказал — слово сработало зря
            if by_word && checked == 0 && x.dialog().user_turns() == 0 && ready_at.elapsed() > Duration::from_secs(5) && !last_voice.is_some_and(|t| t > ready_at) {
                log::info!("Джарвис: ложное срабатывание (никто ничего не сказал) — молчу");
                self.player.stop(true);
                x.dialog().annotate("ложное срабатывание", "");
                x.dialog().finish_turn("");
                return close(&sess);
            }
            let (id, text, since) = x.dialog().utterance();
            if id != 0 && id != checked && since >= Duration::from_millis(350) {
                checked = id;
                if !x.handles_commands() && self.handle(&x, &text, "модель") {
                    return close(&sess);
                }
            }
            if x.dialog().idle(idle) {
                log::info!("разговор: тишина {idle:.0} с — окончен");
                self.player.finish(Duration::from_secs(3));
                x.dialog().finish_turn("");
                return close(&sess);
            }
        }
    }

    /// Законченная фраза человека: «стоп», команды плагинов, Алисе и умному дому — сами, без модели.
    /// true — разговор закончен.
    fn handle(&self, x: &Arc<dyn Session>, text: &str, from: &str) -> bool {
        let cmd = fastcmd::command_for_phrase(text, &self.listener.phrase());
        let quiet = || {
            x.dialog().mute();
            self.player.stop(true);
        };
        let note = |route: &str, answer: &str| {
            x.dialog().annotate(route, answer);
            if x.dialog().turn_text().is_empty() {
                x.dialog().set_text(text);
            }
            x.dialog().finish_turn("");
        };
        if fastcmd::is_stop(&cmd) {
            quiet();
            if fastcmd::is_stop_all(&cmd) {
                self.rt.block_on(self.music.stop_all(&self.host));
                log::info!("Джарвис: «{text}» — стоп ({from})");
            } else {
                log::info!("Джарвис: «{text}» — разговор закрыт, музыка не тронута ({from})");
            }
            note("стоп", "");
            return true;
        }
        let rt = &self.rt;
        if let Some(answer) = rt.block_on(self.music.control(&self.host, &cmd)) {
            quiet();
            note("музыка", &answer);
            (self.say_hook())(answer);
            return true;
        }
        if let Some(answer) = rt.block_on(async { tokio::time::timeout(Duration::from_secs(20), self.host.command(&cmd)).await.ok().flatten() }) {
            quiet();
            log::info!("Джарвис: «{text}» → плагин ({from})");
            note("плагин", &answer);
            if !answer.trim().is_empty() {
                (self.say_hook())(answer);
            } else {
                self.earcon(true);
            }
            return true;
        }
        if let Some(answer) = rt.block_on(self.tools.ha_vacuum(&cmd)) {
            quiet();
            note("Home Assistant", &answer);
            (self.say_hook())(answer);
            return true;
        }
        let t0 = Instant::now();
        if let Some(answer) = rt.block_on(self.tools.smartlife_fast(&cmd)) {
            quiet();
            log::info!("Джарвис: «{text}» → Smart Life ({:.2} с, {from})", t0.elapsed().as_secs_f64());
            note("Smart Life", &answer);
            self.earcon(answer == "Готово.");
            return true;
        }
        // таймеры, будильники, напоминания, музыка — умеет только штатная Алиса; команды умного дома
        // отдаём ей, если так выбрано в настройках
        let mut to_alice = fastcmd::alice_skill_for(&cmd, self.store.with(|s| s.use_home("yandexmusic")));
        if to_alice.is_none() && self.store.with(|s| s.use_home("alice")) {
            to_alice = rt.block_on(self.tools.home_command(&cmd));
        }
        if let Some(a) = to_alice {
            quiet();
            match alice::text(&a, false) {
                Ok(()) => {
                    log::info!("Джарвис: «{a}» → Алисе ({from}, {:.2} с)", t0.elapsed().as_secs_f64());
                    note("умный дом → Алиса", &format!("передал Алисе: «{a}»"));
                    return true;
                }
                Err(e) => log::info!("Алиса: {e} — выполняю сам"),
            }
        }
        if self.store.with(|s| s.use_home("yandex"))
            && let Some(answer) = rt.block_on(self.tools.fast(&cmd))
        {
            quiet();
            log::info!("Джарвис: быстрая команда «{text}» → {answer} (Яндекс {:.2} с, {from})", t0.elapsed().as_secs_f64());
            note("умный дом (быстро)", &answer);
            self.earcon(answer == "Готово.");
            return true;
        }
        false
    }

    /// Сказать вслух просьбу плагина. Пока идёт разговор — не перебиваем его.
    pub fn say_text(&self, text: &str) {
        if self.talking() {
            log::info!("плагин просит сказать «{}» — идёт разговор, пропускаю", crate::plugins::clip(text, 120));
            return;
        }
        (self.say_hook())(text.to_string());
    }

    pub async fn offer_next(self: Arc<Self>, id: String) {
        if self.talking() || self.music.playing() { return; }
        let epoch = self.speech_epoch.load(Ordering::SeqCst);
        let result = self.music.call(&self.host, "related", serde_json::json!({"expected":id})).await;
        let Ok(serde_json::Value::String(answer)) = result else { return };
        if answer.is_empty() || self.talking() || self.music.playing()
            || self.speech_epoch.load(Ordering::SeqCst) != epoch { return; }
        self.journal.note("следующий ролик", "ролик закончился", &answer);
        let _ = tokio::task::spawn_blocking(move || self.say_text(&answer)).await;
    }

    /// Озвучка короткого ответа (плагины, команды, музыка); не вышло — звуком «готово».
    fn say_hook(&self) -> impl Fn(String) + '_ {
        move |answer: String| {
            log::info!("Джарвис: ответ «{}»", crate::plugins::clip(&answer, 200));
            if let Err(e) = self.say_aloud(&answer, Duration::from_secs(25)) {
                log::info!("озвучка: {e}");
                if e != INTERRUPTED {
                    self.earcon(true);
                }
            }
        }
    }

    /// Произнести готовый текст голосом активного ассистента: отдельный короткий звонок, где модель
    /// только читает текст. Блокирует до конца фразы (не дольше limit). Вызывать не из потоков tokio.
    pub fn say_aloud(&self, text: &str, limit: Duration) -> Result<(), String> {
        if self.saying.compare_exchange(false, true, Ordering::SeqCst, Ordering::Relaxed).is_err() {
            return Err(INTERRUPTED.into());
        }
        struct Speaking<'a>(&'a AtomicBool);
        impl Drop for Speaking<'_> { fn drop(&mut self) { self.0.store(false, Ordering::SeqCst); } }
        let _speaking = Speaking(&self.saying);
        let epoch = self.speech_epoch.load(Ordering::SeqCst);
        let s = self.store.get();
        let p = s.active_profile();
        let missing = key_missing(&self.host, &s, &p.engine);
        if !missing.is_empty() {
            return Err(missing);
        }
        let t0 = Instant::now();
        let sess: Arc<dyn Session> = chatgpt::ChatGptSession::open(
            &self.rt,
            chatgpt::Open {
                host: self.host.clone(),
                voice: p.voice.clone(),
                thread: self.chat_thread().into(),
                instructions: "Ты озвучиваешь готовый текст. Произнеси ровно то, что тебе дали, ничего не добавляя, и замолчи.".into(),
                brain: None,
                journal: Arc::new(Journal::default()),
                memory: None,
            },
        );
        let res = self.play_said(&*sess, text, limit, t0, epoch);
        sess.close();
        res
    }

    fn play_said(&self, sess: &dyn Session, text: &str, limit: Duration, t0: Instant, epoch: u64) -> Result<(), String> {
        sess.ready(Duration::from_secs(10).min(limit))?;
        if self.speech_epoch.load(Ordering::SeqCst) != epoch { return Err(INTERRUPTED.into()); }
        sess.say(text)?;
        let out = sess.take_audio().ok_or("нет звука")?;
        let (mut started, mut heard) = (false, false);
        let mut quiet_since = Instant::now();
        while quiet_since.elapsed() < Duration::from_millis(1200) || !heard {
            if self.speech_epoch.load(Ordering::SeqCst) != epoch {
                // «Стоп» из настроек или позвали Алису
                if started {
                    self.player.stop(true);
                }
                return Err(INTERRUPTED.into());
            }
            if t0.elapsed() > limit {
                if started {
                    self.player.stop(true);
                }
                return Err("голос не успел прозвучать".into());
            }
            match out.recv_timeout(Duration::from_millis(100)) {
                Err(RecvTimeoutError::Timeout) => continue,
                Err(RecvTimeoutError::Disconnected) => {
                    if started {
                        self.player.finish(Duration::from_secs(3));
                    }
                    return if heard { Ok(()) } else { Err("голос не прозвучал".into()) };
                }
                Ok(pcm) => {
                    if !started {
                        // плеер — только с первым звуком, а не пока модель думает
                        self.start_voice(sess)?;
                        self.leds.set(Mode::Speaking);
                        let _ = self.player.write(&vec![0; sess.rate() / 4]);
                        started = true;
                    }
                    if self.player.write(&pcm).is_err() && !self.player.playing() {
                        return Err(INTERRUPTED.into()); // плеер забрали — нас перебили
                    }
                    self.leds.level(peak(&pcm) / 9000.0);
                    if loud(&pcm) {
                        heard = true;
                        quiet_since = Instant::now();
                    }
                }
            }
        }
        self.player.finish(Duration::from_secs(3));
        Ok(())
    }

    /// «Джарвис» посреди ответа: замолчать сразу, слушать новую фразу.
    fn interrupt(&self, sess: &Arc<dyn Session>) {
        log::info!("Джарвис: перебили словом — замолкаю");
        sess.dialog().mute();
        self.player.stop(true);
        self.leds.set(Mode::Listening);
    }

    /// Прервать разговор и музыку (Алиса, кнопка «Стоп» в настройках).
    pub fn stop(&self, reason: &str) {
        self.end_chat(reason);
        self.rt.block_on(self.music.stop_all(&self.host));
    }

    /// Закончить только разговор: музыка, если играет, играет дальше. Блокирует до 3 с.
    pub fn end_chat(&self, reason: &str) {
        self.speech_epoch.fetch_add(1, Ordering::SeqCst);
        let sess = {
            let mut st = self.st.lock().unwrap();
            let sess = st.live.take();
            if st.busy || sess.is_some() {
                st.aborted = true;
            }
            if sess.is_none() && st.busy {
                log::info!("Джарвис: остановлен до начала разговора ({reason})");
            }
            sess
        };
        if let Some(sess) = sess {
            log::info!("Джарвис: разговор прерван ({reason})");
            self.player.stop(true);
            sess.close();
        }
    }

    /// Журнал maind: штатный споттер «Алиса» — Джарвис уступает.
    fn watch_alice(self: &Arc<Self>) {
        let w = Arc::downgrade(self);
        alice::follow_log(alice::MAIND_LOG, move |line| {
            let Some(e) = w.upgrade() else { return };
            if line.contains("onPhraseSpotted") && line.contains("alice_capability") && e.talking() {
                e.end_chat("позвали Алису");
                let _ = e.rt.block_on(e.music.call(&e.host, "stop", serde_json::Value::Null));
            }
            if line.contains("Toggle play/pause") {
                e.rt.block_on(e.music.pause_button(&e.host));
            }
            let (started, id) = e.music.alice.lock().unwrap().main_log(line);
            if started || id.is_some() {
                e.rt.clone().spawn(async move {
                    if started { e.music.alice_started(&e.host).await; }
                    if let Some(id) = id && let Some(track) = crate::alice_music::lookup(&id).await {
                        e.music.alice.lock().unwrap().set_track(&id, track);
                    }
                });
            }
        });
        let w = Arc::downgrade(self);
        alice::follow_log("/data/quasar/daemons_logs/mediad.log", move |line| {
            let Some(e) = w.upgrade() else { return };
            let started = e.music.alice.lock().unwrap().media_log(line);
            if started { e.rt.block_on(e.music.alice_started(&e.host)); }
        });
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn helpers() {
        assert!(super::loud(&[0, 700]) && !super::loud(&[600, -600]));
        assert_eq!(super::boost(&[1000], 6)[0], 1995);
        assert_eq!(super::peak(&[-32768, 5]), 32768.0);
    }
}
