//! ChatGPT по подписке: голос (GPT-Live) и «мозг» (Codex) живут в плагине chatgpt, ядро — мост:
//! звук микрофона уходит плагину (voice.feed), голос и расшифровки приходят событиями voice.event.

use crate::{
    journal::Journal,
    plugins::{BoxFut, Host, native::Native},
    session::{Dialog, Session, queue_mic, take_mic},
    settings::{BACKEND_MODELS, Profile},
    tools::Tools,
};
use base64::{Engine as _, engine::general_purpose::STANDARD as B64};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{
        Arc, Condvar, LazyLock, Mutex, Weak,
        atomic::{AtomicBool, AtomicI64, Ordering},
        mpsc::{Receiver, SyncSender, sync_channel},
    },
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

pub const VOICES: &[&str] = &["cove", "juniper", "maple", "spruce", "ember", "vale", "breeze", "arbor", "sol"];
pub const LIVE_MODEL: &str = "gpt-live-1-codex";

/// Лимит подписки кончился — до этого времени (unix, с) отвечаем другим движком.
pub static LIMITED_UNTIL: AtomicI64 = AtomicI64::new(0);
const LIMIT_REST: i64 = 3600;

fn unix() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0)
}

pub fn limited() -> bool {
    unix() < LIMITED_UNTIL.load(Ordering::Relaxed)
}

pub fn voice(v: &str) -> String {
    let v = v.to_lowercase();
    if VOICES.contains(&v.as_str()) { v } else { "cove".into() }
}

pub fn new_id() -> String {
    let mut b = [0u8; 16];
    let _ = getrandom::fill(&mut b);
    let h = hex::encode(b);
    format!("{}-{}-{}-{}-{}", &h[..8], &h[8..12], &h[12..16], &h[16..20], &h[20..])
}

/// Вход в ChatGPT есть: refresh и account.
pub fn signed_in(v: &Value) -> bool {
    ["refresh", "account"].iter().all(|k| v.get(k).and_then(Value::as_str).is_some_and(|s| !s.is_empty()))
}

pub type Brain = Arc<dyn Fn(String) -> BoxFut<Result<String, String>> + Send + Sync>;

static SESSIONS: LazyLock<Mutex<HashMap<String, Weak<ChatGptSession>>>> = LazyLock::new(Mutex::default);

pub struct ChatGptSession {
    id: String,
    dialog: Dialog,
    journal: Arc<Journal>,
    rt: tokio::runtime::Handle,
    brain: Option<Brain>,
    pending: Mutex<Vec<i16>>,
    out_tx: Mutex<Option<SyncSender<Vec<i16>>>>,
    out_rx: Mutex<Option<Receiver<Vec<i16>>>>,
    native: Mutex<Option<Arc<Native>>>,
    started: Mutex<Option<Receiver<Result<(), String>>>>,
    cancelled: AtomicBool,
    wake: tokio::sync::Notify,
    done: (Mutex<bool>, Condvar),
}

pub struct Open {
    pub host: Arc<Host>,
    pub voice: String,
    pub thread: Value,
    pub instructions: String,
    pub brain: Option<Brain>,
    pub journal: Arc<Journal>,
}

impl ChatGptSession {
    pub fn open(rt: &tokio::runtime::Handle, o: Open) -> Arc<ChatGptSession> {
        let (tx, rx) = sync_channel(400);
        let (stx, srx) = sync_channel(1);
        let s = Arc::new(ChatGptSession {
            id: new_id(),
            dialog: Dialog::new(o.journal.clone(), "GPT-Live (subscription)", LIVE_MODEL),
            journal: o.journal,
            rt: rt.clone(),
            brain: o.brain,
            pending: Mutex::default(),
            out_tx: Mutex::new(Some(tx)),
            out_rx: Mutex::new(Some(rx)),
            native: Mutex::default(),
            started: Mutex::new(Some(srx)),
            cancelled: AtomicBool::new(false),
            wake: tokio::sync::Notify::new(),
            done: (Mutex::new(false), Condvar::new()),
        });
        let me = s.clone();
        rt.spawn(async move { me.run(o.host, o.voice, o.thread, o.instructions, stx).await });
        s
    }

    fn cancel(&self) {
        self.cancelled.store(true, Ordering::SeqCst);
        self.wake.notify_one();
    }

    async fn run(self: Arc<Self>, host: Arc<Host>, voice: String, thread: Value, instructions: String, started: SyncSender<Result<(), String>>) {
        let n = match host.provider("chatgpt") {
            Ok(n) => n,
            Err(e) => {
                let _ = started.send(Err(e));
                return self.finish();
            }
        };
        drop(host);
        let pid = n.pid();
        *self.native.lock().unwrap() = Some(n.clone());
        SESSIONS.lock().unwrap().insert(self.id.clone(), Arc::downgrade(&self));
        let t0 = Instant::now();
        let open = async {
            let auth = n.request("auth.token", Value::Null, Duration::from_secs(40)).await?;
            let left = Duration::from_secs(40).saturating_sub(t0.elapsed()).max(Duration::from_secs(1));
            n.request("voice.open", json!({"id": self.id, "profile": {"voice": voice, "thread": thread}, "instructions": instructions, "auth": auth}), left).await
        };
        let res = tokio::select! {
            r = open => r.map(|_| ()),
            _ = self.wake.notified() => Err("разговор отменён".into()),
        };
        let ok = res.is_ok();
        let _ = started.send(res);
        while ok && !self.cancelled.load(Ordering::SeqCst) {
            tokio::select! {
                _ = self.wake.notified() => continue,
                _ = tokio::time::sleep(Duration::from_millis(100)) => {}
            }
            if n.pid() != pid {
                break; // плагин перезапустился — разговора на той стороне больше нет
            }
            let pcm = take_mic(&mut self.pending.lock().unwrap(), 1600);
            if pcm.is_empty() {
                continue;
            }
            let bytes: Vec<u8> = pcm.iter().flat_map(|v| v.to_le_bytes()).collect();
            if n.notify("voice.feed", json!({"id": self.id, "pcm": B64.encode(bytes)})).await.is_err() {
                break;
            }
        }
        self.out_tx.lock().unwrap().take(); // канал голоса закрывается — движок поймёт, что разговор оборвался
        SESSIONS.lock().unwrap().remove(&self.id);
        let _ = n.request("voice.close", json!({"id": self.id}), Duration::from_secs(1)).await;
        self.finish();
    }

    fn finish(&self) {
        self.out_tx.lock().unwrap().take();
        *self.done.0.lock().unwrap() = true;
        self.done.1.notify_all();
    }

    /// Разговор до этого — для задачи бэкенду (без общей памяти: последние полчаса журнала).
    fn with_history(&self, task: &str) -> String {
        let now = crate::journal::now_s();
        let mut lines = vec![];
        for t in self.journal.items(16).iter().rev() {
            if now - t.ts > 1800.0 {
                continue;
            }
            if !t.text.is_empty() {
                lines.push(format!("Человек: {}", t.text));
            }
            if !t.answer.is_empty() {
                lines.push(format!("Джарвис: {}", t.answer));
            }
        }
        if lines.is_empty() {
            return task.into();
        }
        let from = lines.len().saturating_sub(20);
        format!("Разговор до этого (для контекста):\n{}\n\nЗадача от голосового ассистента — выполни её с учётом разговора выше: {task}", lines[from..].join("\n"))
    }
}

impl Session for ChatGptSession {
    fn feed(&self, pcm: &[i16]) {
        queue_mic(&mut self.pending.lock().unwrap(), pcm);
    }
    fn take_audio(&self) -> Option<Receiver<Vec<i16>>> {
        self.out_rx.lock().unwrap().take()
    }
    fn rate(&self) -> usize {
        jarvis_dsp::audio::OUT_RATE
    }
    fn out_boost(&self) -> f64 {
        4.5
    }
    fn ready(&self, timeout: Duration) -> Result<(), String> {
        let Some(rx) = self.started.lock().unwrap().take() else { return Ok(()) };
        match rx.recv_timeout(timeout) {
            Ok(r) => r,
            Err(_) => Err("ChatGPT timeout".into()),
        }
    }
    fn say(&self, text: &str) -> Result<(), String> {
        let n = self.native.lock().unwrap().clone().ok_or("ChatGPT not ready")?;
        let p = json!({"id": self.id, "text": text});
        self.rt.block_on(async move { n.request("voice.say", p, Duration::from_secs(20)).await }).map(|_| ())
    }
    fn close(&self) {
        self.cancel();
        let g = self.done.0.lock().unwrap();
        let _ = self.done.1.wait_timeout_while(g, Duration::from_secs(3), |d| !*d);
    }
    fn dialog(&self) -> &Dialog {
        &self.dialog
    }
}

/// Событие голосовой сессии от плагина chatgpt.
pub fn voice_event(n: &Arc<Native>, p: Value) {
    let id = p.get("id").or(p.get("ID")).and_then(Value::as_str).unwrap_or("");
    let kind = p.get("kind").or(p.get("Kind")).and_then(Value::as_str).unwrap_or("");
    let value = p.get("value").or(p.get("Value")).cloned().unwrap_or(Value::Null);
    let Some(s) = SESSIONS.lock().unwrap().get(id).and_then(Weak::upgrade) else { return };
    if s.cancelled.load(Ordering::SeqCst) || !s.native.lock().unwrap().as_ref().is_some_and(|x| Arc::ptr_eq(x, n)) {
        return;
    }
    let text = value.as_str().unwrap_or("");
    match kind {
        "user" => s.dialog.on_user(text),
        "bot" => s.dialog.on_bot(text),
        "heard" => s.dialog.heard_voice(),
        "working" => s.dialog.working(value.as_i64().unwrap_or(0) as i32),
        "limit" => {
            LIMITED_UNTIL.store(unix() + LIMIT_REST, Ordering::Relaxed);
            s.cancel();
        }
        "error" => s.dialog.finish_turn(text),
        "closed" => s.cancel(),
        "audio" => {
            let Ok(data) = B64.decode(text) else { return };
            if data.len() % 2 != 0 {
                return;
            }
            let pcm = data.chunks_exact(2).map(|c| i16::from_le_bytes([c[0], c[1]])).collect();
            if let Some(tx) = s.out_tx.lock().unwrap().as_ref() {
                let _ = tx.try_send(pcm);
            }
        }
        _ => {}
    }
}

/// Плагин chatgpt просит бэкенд выполнить задачу голосовой модели (voice.brain).
pub async fn brain_request(params: Value) -> Result<Value, String> {
    let id = params.get("id").or(params.get("ID")).and_then(Value::as_str).unwrap_or("");
    let task = params.get("task").or(params.get("Task")).and_then(Value::as_str).unwrap_or("");
    let s = SESSIONS.lock().unwrap().get(id).and_then(Weak::upgrade).ok_or("session closed")?;
    let brain = s.brain.clone().ok_or("brain unavailable")?;
    let task = s.with_history(task);
    drop(s);
    match tokio::time::timeout(Duration::from_secs(45), brain(task)).await {
        Ok(r) => r.map(Value::String),
        Err(_) => Err("бэкенд не ответил за 45 с".into()),
    }
}

/// Уровни рассуждения, которые принимает модель (Sol 6.1 и Astra не знают "none").
pub fn effort(model: &str, effort: &str) -> String {
    let e = effort.trim().to_lowercase();
    let allowed: &[&str] = match model.trim().to_lowercase().as_str() {
        "gpt-6.1-sol" | "gpt-6-astra" => &["low", "medium", "high"],
        _ => &["none", "low", "medium", "high"],
    };
    if allowed.contains(&e.as_str()) { e } else { "low".into() }
}

/// Текстовый запрос к модели подписки через плагин (Codex) с инструментами колонки.
pub async fn ask_codex(host: &Host, auth: Value, p: &Profile, instructions: &str, text: &str, tools: Option<&Tools>) -> Result<String, String> {
    let model = if p.backend_model.is_empty() { BACKEND_MODELS[0].to_string() } else { p.backend_model.clone() };
    let effort = effort(&model, &p.effort);
    let mut defs = match tools {
        Some(t) => t.definitions().await,
        None => vec![],
    };
    if p.web_search {
        defs.push(json!({"type": "web_search"}));
    }
    let mut input = vec![json!({"type": "message", "role": "user", "content": [{"type": "input_text", "text": text}]})];
    let session = new_id();
    for _ in 0..5 {
        let n = host.provider("chatgpt")?;
        let body = json!({"model": model, "instructions": instructions, "input": input, "tools": defs, "tool_choice": "auto",
            "parallel_tool_calls": false, "reasoning": {"effort": effort}, "store": false, "stream": true, "include": []});
        let r = n.request("codex.request", json!({"auth": auth, "session": session, "body": body}), Duration::from_secs(60)).await?;
        let answer = r.get("answer").or(r.get("Answer")).and_then(Value::as_str).unwrap_or("").trim().to_string();
        let calls = r.get("calls").or(r.get("Calls")).and_then(Value::as_array).cloned().unwrap_or_default();
        if calls.is_empty() {
            return if answer.is_empty() { Err("модель ничего не ответила".into()) } else { Ok(answer) };
        }
        let Some(tools) = tools else { return Err("модель попросила инструмент, которого нет".into()) };
        for c in calls {
            let name = c.get("name").and_then(Value::as_str).unwrap_or("");
            let args = c.get("arguments").and_then(Value::as_str).unwrap_or("");
            let result = tokio::time::timeout(Duration::from_secs(15), tools.call(name, args)).await.unwrap_or_else(|_| "Не успел: инструмент не ответил за 15 с.".into());
            log::info!("подписка: {name}({args}) → {result}");
            input.push(json!({"type": "function_call", "name": c["name"], "arguments": c["arguments"], "call_id": c["call_id"]}));
            input.push(json!({"type": "function_call_output", "call_id": c["call_id"], "output": result}));
        }
    }
    Err("модель не закончила за 5 шагов".into())
}

#[cfg(test)]
mod tests {
    #[test]
    fn efforts_and_ids() {
        assert_eq!(super::effort("gpt-6.1-sol", "none"), "low");
        assert_eq!(super::effort("gpt-6-luna", " High "), "high");
        let id = super::new_id();
        assert_eq!(id.len(), 36);
        assert_eq!(super::voice("Ember"), "ember");
        assert_eq!(super::voice("x"), "cove");
    }
}
