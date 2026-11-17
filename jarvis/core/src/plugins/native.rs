//! Нативный плагин: отдельная программа под архитектуру колонки. Ядро запускает её и говорит с ней строками
//! JSON через stdin/stdout (одна строка — одно сообщение), stderr идёт в журнал. Упала — перезапуск с паузой,
//! пять падений за 10 минут — плагин остаётся выключенным до перезапуска Джарвиса. Протокол — тот же, что
//! у Go-версии (pluginsdk), поэтому старые плагины работают без пересборки.
//!
//!   ядро → плагин: init {api, version, arch, dir, data, settings, info} → ok;  tool {name, args} → строка;
//!                  command {text, raw} → {handled, answer};  settings {settings};  cancel {id};  shutdown
//!   плагин → ядро: register {tools, commands, http};  status {text};  log {text};  say {text};
//!                  alice {text, silent} → bool;  core {method, params} → ответ;  event {name, data}

use super::{Host, Manifest, PLUGIN_API, arch, clip, valid_tool_name};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use std::{
    collections::HashMap,
    path::PathBuf,
    process::Stdio,
    sync::{
        Arc, Mutex, Weak,
        atomic::{AtomicI64, Ordering},
    },
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    process::{ChildStdin, Command},
    sync::{Notify, oneshot, watch},
};

const LINE_MAX: usize = 4 << 20;

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Msg {
    #[serde(skip_serializing_if = "is_zero")]
    pub deadline: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub id: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub method: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub params: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

struct Proc {
    stdin: tokio::sync::Mutex<ChildStdin>,
    done: watch::Receiver<bool>,
    pid: u32,
    kill: Arc<Notify>,
}

#[derive(Default)]
struct St {
    settings: Map<String, Value>,
    tools: Vec<Value>,
    commands: bool,
    status: String,
    fails: Vec<Instant>,
    stopped: bool,
    proc: Option<Arc<Proc>>,
    http: Option<(u16, String)>,
}

pub struct Native {
    host: Weak<Host>,
    pub m: Manifest,
    pub dir: PathBuf,
    bin: PathBuf,
    bundled: bool,
    st: Mutex<St>,
    next_id: AtomicI64,
    pending: Mutex<HashMap<i64, oneshot::Sender<Msg>>>,
}

/// Дождаться завершения процесса (без удержания блокировки канала через await).
async fn wait_done(mut rx: watch::Receiver<bool>) {
    let _ = rx.wait_for(|d| *d).await.map(|_| ());
}

fn now_ms() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis() as i64).unwrap_or(0)
}

/// Фраза для шаблонов плагинов: без имени, нижний регистр, ё → е, без знаков препинания.
pub fn command_text(text: &str) -> String {
    let s: String = text
        .to_lowercase()
        .replace('ё', "е")
        .chars()
        .map(|c| if c.is_alphanumeric() || c.is_whitespace() || c == '-' { c } else { ' ' })
        .collect();
    let mut s = s.split_whitespace().collect::<Vec<_>>().join(" ");
    for name in ["джарвис ", "jarvis "] {
        if let Some(rest) = s.strip_prefix(name) {
            s = rest.to_string();
        }
    }
    s.trim().to_string()
}

impl Native {
    pub async fn start(host: Arc<Host>, m: Manifest, dir: PathBuf, settings: Map<String, Value>, official: bool) -> Result<Arc<Native>, String> {
        let main = m.main.replace("{arch}", arch());
        let mut bin = dir.join(&main);
        let mut bundled = false;
        if !bin.exists() {
            if let (true, Some(modules)) = (official, host.modules_bin.as_ref().filter(|p| p.exists())) {
                bin = modules.clone(); // модуль вшит в старый jarvisd: он отдаст его по JARVIS_PLUGIN_ID
                bundled = true;
            } else {
                return Err(format!("нет программы плагина для этой колонки ({main})"));
            }
        }
        let n = Arc::new(Native {
            host: Arc::downgrade(&host),
            m,
            dir,
            bin,
            bundled,
            st: Mutex::new(St { settings, ..Default::default() }),
            next_id: AtomicI64::new(0),
            pending: Mutex::default(),
        });
        n.clone().spawn().await?;
        Ok(n)
    }

    fn spawn(self: Arc<Self>) -> super::BoxFut<Result<(), String>> {
        Box::pin(self.spawn_inner())
    }

    async fn spawn_inner(self: Arc<Self>) -> Result<(), String> {
        let data = self.dir.join("data");
        let _ = std::fs::create_dir_all(&data);
        let mut cmd = Command::new(&self.bin);
        cmd.current_dir(&self.dir)
            .env("JARVIS_PLUGIN", "1")
            .env("JARVIS_PLUGIN_ID", &self.m.id)
            .env("JARVIS_PLUGIN_DIR", &self.dir)
            .env("JARVIS_PLUGIN_DATA", &data)
            .env("JARVIS_PLUGIN_API", PLUGIN_API.to_string())
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        #[cfg(target_os = "linux")]
        unsafe {
            // плагин умирает вместе с ядром (не остаётся сиротой после обновления или падения)
            cmd.pre_exec(|| {
                libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL);
                Ok(())
            });
        }
        let mut child = cmd.spawn().map_err(|e| format!("запуск: {e}"))?;
        let stdin = child.stdin.take().unwrap();
        let stdout = child.stdout.take().unwrap();
        let stderr = child.stderr.take().unwrap();
        let (done_tx, done_rx) = watch::channel(false);
        let kill = Arc::new(Notify::new());
        let proc = Arc::new(Proc { stdin: tokio::sync::Mutex::new(stdin), done: done_rx, pid: child.id().unwrap_or(0), kill: kill.clone() });
        {
            let mut st = self.st.lock().unwrap();
            st.proc = Some(proc.clone());
            st.tools.clear();
            st.commands = false;
        }
        let id = self.m.id.clone();
        tokio::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(l)) = lines.next_line().await {
                log::info!("плагин {id}: {}", clip(&l, 500));
            }
        });
        let me = self.clone();
        tokio::spawn(async move { me.read(stdout).await });
        let me = self.clone();
        tokio::spawn(async move {
            let status = tokio::select! {
                s = child.wait() => s,
                _ = kill.notified() => { let _ = child.start_kill(); child.wait().await }
            };
            let _ = done_tx.send(true);
            me.exited(status.map(|s| s.to_string()).unwrap_or_else(|e| e.to_string())).await;
        });
        let host = self.host.upgrade().ok_or("ядро завершается")?;
        let settings = self.st.lock().unwrap().settings.clone();
        let init = json!({"api": PLUGIN_API, "version": crate::version(), "arch": arch(), "dir": self.dir, "data": data,
                          "settings": settings, "info": (host.hooks.info)()});
        drop(host);
        if let Err(e) = self.request("init", init, Duration::from_secs(15)).await {
            self.kill().await;
            return Err(format!("плагин не ответил на init: {e}"));
        }
        Ok(())
    }

    async fn read(self: Arc<Self>, stdout: tokio::process::ChildStdout) {
        let mut r = BufReader::with_capacity(64 << 10, stdout);
        let mut line = Vec::new();
        loop {
            line.clear();
            match r.read_until(b'\n', &mut line).await {
                Ok(0) | Err(_) => return,
                Ok(_) => {}
            }
            if line.len() > LINE_MAX {
                log::info!("плагин {}: слишком длинное сообщение — пропущено", self.m.id);
                continue;
            }
            let Ok(m) = serde_json::from_slice::<Msg>(&line) else {
                log::info!("плагин {}: не JSON в stdout: {}", self.m.id, clip(&String::from_utf8_lossy(&line), 200));
                continue;
            };
            if m.method.is_empty() {
                if let Some(tx) = self.pending.lock().unwrap().remove(&m.id) {
                    let _ = tx.send(m);
                }
                continue;
            }
            if m.method == "voice.event" {
                continue; // голосовые сессии модулей — когда перенесём движки
            }
            let me = self.clone();
            tokio::spawn(async move { me.handle(m).await });
        }
    }

    /// Запрос или уведомление от плагина.
    async fn handle(self: Arc<Self>, m: Msg) {
        let p = m.params.clone().unwrap_or(Value::Null);
        let text = p.get("text").and_then(Value::as_str).unwrap_or("").to_string();
        let mut result = Value::Null;
        let mut err = String::new();
        let Some(host) = self.host.upgrade() else { return };
        match m.method.as_str() {
            "register" => {
                let mut tools = vec![];
                for t in p.get("tools").and_then(Value::as_array).cloned().unwrap_or_default() {
                    let name = t.get("name").and_then(Value::as_str).unwrap_or("").to_string();
                    let Value::Object(mut def) = t else { continue };
                    if !valid_tool_name(&name) {
                        log::info!("плагин {}: инструмент {name:?} отклонён", self.m.id);
                        continue;
                    }
                    def.insert("type".into(), "function".into());
                    tools.push(Value::Object(def));
                }
                let mut st = self.st.lock().unwrap();
                st.tools = tools;
                st.commands = p.get("commands").and_then(Value::as_bool).unwrap_or(false);
                let port = p.pointer("/http/port").and_then(Value::as_u64).unwrap_or(0);
                let token = p.pointer("/http/token").and_then(Value::as_str).unwrap_or("");
                if (1..65536).contains(&port) && token.len() >= 16 {
                    st.http = Some((port as u16, token.to_string()));
                }
                result = true.into();
            }
            "status" => self.st.lock().unwrap().status = clip(&text, 200),
            "log" => log::info!("плагин {}: {}", self.m.id, clip(&text, 500)),
            "say" => {
                if !text.trim().is_empty() {
                    (host.hooks.say)(clip(&text, 600));
                }
                result = true.into();
            }
            "alice" => {
                let silent = p.get("silent").and_then(Value::as_bool).unwrap_or(false);
                result = (!text.trim().is_empty() && (host.hooks.alice)(clip(&text, 300), silent).await).into();
            }
            "core" => {
                let method = p.get("method").and_then(Value::as_str).unwrap_or("").to_string();
                let params = p.get("params").cloned().unwrap_or(Value::Null);
                match (host.hooks.request)(self.m.id.clone(), method, params).await {
                    Ok(v) => result = v,
                    Err(e) => err = e,
                }
            }
            "event" => {
                let name = p.get("name").and_then(Value::as_str).unwrap_or("");
                if !name.is_empty() && name != "exit" {
                    (host.hooks.event)(&self.m, name, p.get("data").cloned().unwrap_or(Value::Null));
                }
                result = true.into();
            }
            other => err = format!("неизвестный метод {other}"),
        }
        if m.id != 0 {
            let _ = self.send(Msg { id: m.id, result: Some(result), error: err, ..Default::default() }).await;
        }
    }

    async fn send(&self, m: Msg) -> Result<(), String> {
        let proc = self.st.lock().unwrap().proc.clone().ok_or("плагин не запущен")?;
        let mut b = serde_json::to_vec(&m).map_err(|e| e.to_string())?;
        b.push(b'\n');
        let mut w = proc.stdin.lock().await;
        w.write_all(&b).await.map_err(|e| e.to_string())?;
        w.flush().await.map_err(|e| e.to_string())
    }

    pub async fn request(&self, method: &str, params: Value, timeout: Duration) -> Result<Value, String> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed) + 1;
        let (tx, rx) = oneshot::channel();
        self.pending.lock().unwrap().insert(id, tx);
        let done = match self.st.lock().unwrap().proc.as_ref() {
            Some(p) => p.done.clone(),
            None => {
                self.pending.lock().unwrap().remove(&id);
                return Err("плагин не запущен".into());
            }
        };
        let msg = Msg { id, method: method.into(), params: Some(params), deadline: now_ms() + timeout.as_millis() as i64, ..Default::default() };
        if let Err(e) = self.send(msg).await {
            self.pending.lock().unwrap().remove(&id);
            return Err(e);
        }
        let res = tokio::select! {
            r = rx => r.map_err(|_| "плагин завершился".to_string()),
            _ = wait_done(done) => Err("плагин завершился".to_string()),
            _ = tokio::time::sleep(timeout) => {
                let _ = self.send(Msg { method: "cancel".into(), params: Some(json!({"id": id})), ..Default::default() }).await;
                Err("плагин не ответил вовремя".to_string())
            }
        };
        self.pending.lock().unwrap().remove(&id);
        let m = res?;
        if !m.error.is_empty() {
            return Err(m.error);
        }
        Ok(m.result.unwrap_or(Value::Null))
    }

    async fn exited(self: Arc<Self>, why: String) {
        if let Some(host) = self.host.upgrade() {
            (host.hooks.event)(&self.m, "exit", Value::Null);
        }
        let tries = {
            let mut st = self.st.lock().unwrap();
            st.proc = None;
            st.http = None;
            if st.stopped {
                return;
            }
            let now = Instant::now();
            st.fails.retain(|t| now.duration_since(*t) < Duration::from_secs(600));
            st.fails.push(now);
            st.fails.len()
        };
        self.pending.lock().unwrap().clear();
        if tries >= 5 {
            log::info!("плагин {}: падает раз за разом ({why}) — больше не перезапускаю", self.m.id);
            self.st.lock().unwrap().status = "плагин падает раз за разом — выключите и включите его, когда исправят".into();
            return;
        }
        let wait = [2, 5, 15, 60][(tries - 1).min(3)];
        log::info!("плагин {} завершился ({why}) — перезапуск через {wait} с", self.m.id);
        tokio::spawn(async move {
            tokio::time::sleep(Duration::from_secs(wait)).await;
            if self.st.lock().unwrap().stopped {
                return;
            }
            if let Err(e) = self.clone().spawn().await {
                log::info!("плагин {}: перезапуск не удался: {e}", self.m.id);
            }
        });
    }

    async fn kill(&self) {
        let Some(proc) = self.st.lock().unwrap().proc.clone() else { return };
        #[cfg(unix)]
        if proc.pid != 0 {
            unsafe { libc::kill(proc.pid as i32, libc::SIGTERM) };
            if tokio::time::timeout(Duration::from_secs(3), wait_done(proc.done.clone())).await.is_ok() {
                return;
            }
        }
        proc.kill.notify_one();
        let _ = tokio::time::timeout(Duration::from_secs(3), wait_done(proc.done.clone())).await;
    }

    pub fn tools(&self) -> Vec<Value> {
        self.st.lock().unwrap().tools.clone()
    }

    pub async fn call_tool(&self, name: &str, args: &str) -> Option<String> {
        if !self.tools().iter().any(|t| t.get("name").and_then(Value::as_str) == Some(name)) {
            return None;
        }
        let args: Value = serde_json::from_str(args).ok().filter(Value::is_object).unwrap_or(json!({}));
        match self.request("tool", json!({"name": name, "args": args}), Duration::from_secs(60)).await {
            Ok(Value::String(s)) => Some(clip(&s, 8000)),
            Ok(v) => Some(clip(&v.to_string(), 8000)),
            Err(e) => Some(format!("Плагин «{}» не справился: {}", self.m.name, clip(&e, 200))),
        }
    }

    pub async fn command(&self, text: &str) -> Option<String> {
        if !self.st.lock().unwrap().commands {
            return None;
        }
        // быстрые команды — быстрые: не успел — пусть думает модель
        let r = self.request("command", json!({"text": command_text(text), "raw": text}), Duration::from_secs(3)).await.ok()?;
        r.get("handled").and_then(Value::as_bool).filter(|h| *h)?;
        Some(clip(r.get("answer").and_then(Value::as_str).unwrap_or(""), 600))
    }

    pub fn configure(self: &Arc<Self>, settings: Map<String, Value>) {
        self.st.lock().unwrap().settings = settings.clone();
        let me = self.clone();
        tokio::spawn(async move {
            let _ = me.send(Msg { method: "settings".into(), params: Some(json!({"settings": settings})), ..Default::default() }).await;
        });
    }

    pub fn status(&self) -> String {
        self.st.lock().unwrap().status.clone()
    }

    pub fn http(&self) -> Option<(u16, String)> {
        self.st.lock().unwrap().http.clone()
    }

    pub fn bundled(&self) -> bool {
        self.bundled
    }

    pub async fn stop(&self) {
        let proc = {
            let mut st = self.st.lock().unwrap();
            st.stopped = true;
            st.proc.clone()
        };
        let Some(proc) = proc else { return };
        let _ = self.send(Msg { method: "shutdown".into(), ..Default::default() }).await;
        // даём завершиться самому: сохранить данные, закрыть соединения
        if tokio::time::timeout(super::SHUTDOWN_WAIT, wait_done(proc.done.clone())).await.is_ok() {
            return;
        }
        self.kill().await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn command_text_like_go() {
        assert_eq!(command_text("Джарвис, подбрось Монетку!"), "подбрось монетку");
        assert_eq!(command_text("ёлка"), "елка");
    }

    #[test]
    fn msg_shape() {
        let m = Msg { id: 3, method: "tool".into(), params: Some(json!({"a": 1})), ..Default::default() };
        assert_eq!(serde_json::to_string(&m).unwrap(), r#"{"id":3,"method":"tool","params":{"a":1}}"#);
        let r: Msg = serde_json::from_str(r#"{"id":3,"result":"ok"}"#).unwrap();
        assert_eq!(r.result, Some(json!("ok")));
    }
}
