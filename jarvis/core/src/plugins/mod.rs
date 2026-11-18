//! Плагины. Лежат в <data>/plugins/<id>/, включённость и настройки — в <data>/plugins.json (0600).
//! Формат папок, plugin.json и plugins.json — тот же, что у Go-версии.
//!
//! Виды:
//!   - native — отдельная программа: говорит с ядром строками JSON через stdin/stdout (native.rs, протокол pluginsdk);
//!   - lua — скрипт с ограничениями памяти и выполнения внутри ядра (feature lua);
//!   - js — старые лёгкие плагины: пока не запускаются (ждут перевода на Lua).
//! Официальные модули Go-версии (chatgpt, home, vpn, youtube…) на переходный период запускает
//! старый бинарник jarvisd (флаг -modules): он сам отдаёт нужный модуль по JARVIS_PLUGIN_ID.

pub mod catalog;
pub mod coresvc;
pub mod install;
pub mod native;
mod modules;
#[cfg(feature = "lua")]
mod lua;
#[cfg(test)]
mod protocol_tests;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use std::{
    collections::BTreeMap,
    future::Future,
    path::{Path, PathBuf},
    pin::Pin,
    sync::{Arc, Mutex},
    time::Duration,
};

pub const PLUGIN_API: i64 = 1;

pub fn compiled_modules() -> Vec<String> { modules::manifests().into_iter().map(|m|m.id).collect() }

pub fn valid_id(id: &str) -> bool {
    let b = id.as_bytes();
    (2..=32).contains(&b.len())
        && (b[0].is_ascii_lowercase() || b[0].is_ascii_digit())
        && b.iter().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || *c == b'-')
}

pub const CATEGORIES: &[(&str, &str, &str)] = &[
    ("assistant", "Ассистенты и голоса", "голосовые движки, нейросети, голоса"),
    ("home", "Умный дом", "свет, розетки, датчики, сценарии"),
    ("media", "Музыка и видео", "музыка, радио, подкасты, видео"),
    ("info", "Погода и справки", "погода, курсы, новости, транспорт"),
    ("productivity", "Дела и заметки", "списки, напоминания, календари, почта"),
    ("dev", "Кодинг и IT", "серверы, GitHub, мониторинг, сеть"),
    ("fun", "Игры и развлечения", "игры, шутки, викторины, случайности"),
    ("system", "Колонка и система", "подсветка, кнопки, слово активации, VPN"),
    ("other", "Другое", "всё, что не подошло выше"),
];

pub fn valid_category(id: &str) -> bool {
    CATEGORIES.iter().any(|c| c.0 == id)
}

pub fn categories_json() -> Value {
    CATEGORIES.iter().map(|c| json!({"id": c.0, "name": c.1, "hint": c.2})).collect()
}

/// Инструменты ядра: плагин не может их подменить.
pub const CORE_TOOLS: &[&str] = &[
    "smart_home", "play_music", "music_control", "alice_command", "recall_conversation", "edit_memory",
    "home_assistant", "xiaomi_home", "smart_life",
];

pub fn valid_tool_name(n: &str) -> bool {
    (1..=64).contains(&n.len()) && n.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-') && !CORE_TOOLS.contains(&n)
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Setting {
    pub key: String,
    pub label: String,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hint: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub options: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub default: Option<Value>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub required: bool,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Page {
    pub id: String,
    pub title: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub icon: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub group: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Manifest {
    pub id: String,
    pub name: String,
    pub version: String,
    pub description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub author: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub homepage: String,
    pub kind: String,
    pub main: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub api: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub icon: String,
    pub category: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub provides: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub settings: Vec<Setting>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub min_core: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub pages: Vec<Page>,
    /// Права Lua-плагина (что ему можно): network, say, tools, commands…
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub permissions: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub network_allow: Vec<String>,
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

impl Manifest {
    pub fn provides(&self, cap: &str) -> bool {
        self.provides.iter().any(|c| c == cap)
    }

    pub fn validate(&self) -> Result<(), String> {
        let integ = ["homeassistant", "xiaomi", "smartlife", "yandexhome"];
        if !valid_id(&self.id) {
            return Err("id плагина: латиница в нижнем регистре, цифры и дефис, 2–32 символа".into());
        }
        if self.name.trim().is_empty() {
            return Err("у плагина нет name".into());
        }
        if !["js", "native", "lua", "module"].contains(&self.kind.as_str()) {
            return Err(r#"kind плагина — "lua", "js", "native" или "module""#.into());
        }
        if !valid_category(&self.category) {
            let ids: Vec<_> = CATEGORIES.iter().map(|c| c.0).collect();
            return Err(format!("category плагина — одна из: {}", ids.join(", ")));
        }
        if self.provides("vpn") && !["native","module"].contains(&self.kind.as_str()) {
            return Err("провайдер VPN должен быть нативным плагином".into());
        }
        if !["native","module"].contains(&self.kind.as_str()) && integ.iter().any(|c| self.provides(c)) {
            return Err("провайдер интеграции должен быть нативным плагином".into());
        }
        if self.main.is_empty() || self.main.contains("..") || self.main.starts_with('/') || self.main.contains('\\') {
            return Err("main плагина — имя файла внутри архива".into());
        }
        if self.kind == "module" && (self.id != self.main || self.provides != [self.main.clone()]) {
            return Err("Манифест встроенного модуля должен указывать его собственные id, main и provides".into());
        }
        if self.api > PLUGIN_API {
            return Err(format!("плагин написан под API {}, а этот Джарвис знает {PLUGIN_API} — обновите Джарвиса", self.api));
        }
        Ok(())
    }

    pub fn needs_newer_core(&self) -> bool {
        let v = crate::version();
        !self.min_core.is_empty() && v != "dev" && newer(&self.min_core, v)
    }
}

/// a новее b (0.9.10 > 0.9.9; хвост «-rs» и т.п. не считается).
pub fn newer(a: &str, b: &str) -> bool {
    let parse = |s: &str| -> Vec<u64> {
        s.trim_start_matches('v').split(['-', '+']).next().unwrap_or("").split('.').map(|x| x.parse().unwrap_or(0)).collect()
    };
    let (a, b) = (parse(a), parse(b));
    for i in 0..a.len().max(b.len()) {
        let (x, y) = (a.get(i).copied().unwrap_or(0), b.get(i).copied().unwrap_or(0));
        if x != y {
            return x > y;
        }
    }
    false
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default)]
pub struct PluginState {
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub removed: bool,
    pub enabled: bool,
    pub source: String,
    pub official: bool,
    pub installed_at: String,
    #[serde(skip_serializing_if = "Map::is_empty")]
    pub settings: Map<String, Value>,
}

impl Default for PluginState {
    fn default() -> Self {
        PluginState { removed: false, enabled: true, source: "file".into(), official: false, installed_at: now_rfc3339(), settings: Map::new() }
    }
}

pub fn now_rfc3339() -> String {
    jiff::Timestamp::now().to_string()
}

pub type BoxFut<T> = Pin<Box<dyn Future<Output = T> + Send>>;

/// Что ядро даёт плагинам.
pub struct Hooks {
    pub network: Box<dyn Fn() -> Value + Send + Sync>,
    /// Сервисы ядра для плагина: (id, method, params) → ответ.
    pub request: Box<dyn Fn(String, String, Value) -> BoxFut<Result<Value, String>> + Send + Sync>,
    /// Событие плагина; "exit" — процесс завершился.
    pub event: Box<dyn Fn(&Manifest, &str, Value) + Send + Sync>,
    pub say: Box<dyn Fn(String) + Send + Sync>,
    pub alice: Box<dyn Fn(String, bool) -> BoxFut<bool> + Send + Sync>,
    pub info: Box<dyn Fn() -> Value + Send + Sync>,
}

impl Default for Hooks {
    fn default() -> Self {
        Hooks {
            network: Box::new(|| json!({})),
            request: Box::new(|_, _, _| Box::pin(async { Err("метод ядра недоступен".to_string()) })),
            event: Box::new(|_, _, _| {}),
            say: Box::new(|_| {}),
            alice: Box::new(|_, _| Box::pin(async { false })),
            info: Box::new(|| json!({})),
        }
    }
}

#[derive(Clone)]
pub enum Runtime {
    Native(Arc<native::Native>),
    Module(Arc<modules::Module>),
    #[cfg(feature = "lua")]
    Lua(Arc<lua::Script>),
}

impl Runtime {
    pub fn tools(&self) -> Vec<Value> {
        match self {
            Runtime::Native(n) => n.tools(),
            Runtime::Module(_) => vec![],
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.tools(),
        }
    }
    pub async fn call_tool(&self, name: &str, args: &str) -> Option<String> {
        match self {
            Runtime::Native(n) => n.call_tool(name, args).await,
            Runtime::Module(_) => None,
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.call_tool(name, args).await,
        }
    }
    pub async fn command(&self, text: &str) -> Option<String> {
        match self {
            Runtime::Native(n) => n.command(text).await,
            Runtime::Module(_) => None,
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.command(text).await,
        }
    }
    pub fn configure(&self, settings: Map<String, Value>) {
        match self {
            Runtime::Native(n) => n.configure(settings),
            Runtime::Module(_) => {},
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.configure(settings),
        }
    }
    pub fn status(&self) -> String {
        match self {
            Runtime::Native(n) => n.status(),
            Runtime::Module(_) => "Встроенный модуль Rust".into(),
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.status(),
        }
    }
    pub async fn stop(&self) {
        match self {
            Runtime::Native(n) => n.stop().await,
            Runtime::Module(n) => n.stop().await,
            #[cfg(feature = "lua")]
            Runtime::Lua(n) => n.stop().await,
        }
    }
    pub fn native(&self) -> Option<&Arc<native::Native>> {
        match self {
            Runtime::Native(n) => Some(n),
            Runtime::Module(_) => None,
            #[cfg(feature = "lua")]
            Runtime::Lua(_) => None,
        }
    }
    pub async fn request(&self, method:&str, params:Value, timeout:Duration)->Result<Value,String> {
        match self {
            Self::Native(n)=>n.request(method,params,timeout).await,
            Self::Module(n)=>n.request(method,params,timeout).await,
            #[cfg(feature="lua")]
            Self::Lua(_)=>Err("Lua-плагин не является провайдером подключения".into()),
        }
    }
    pub async fn notify(&self, method:&str, params:Value)->Result<(),String> {
        match self {
            Self::Native(n)=>n.notify(method,params).await,
            _=>self.request(method,params,Duration::from_secs(3)).await.map(|_|()),
        }
    }
}

pub struct Plugin {
    pub manifest: Manifest,
    pub dir: PathBuf,
    pub rt: Option<Runtime>,
    pub err: String,
}

#[derive(Default)]
pub struct Inner {
    pub plugins: BTreeMap<String, Plugin>,
    pub state: BTreeMap<String, PluginState>,
}

pub struct Host {
    pub data_dir: PathBuf,
    pub hooks: Hooks,
    /// Старый jarvisd с вшитыми модулями Go (переходный период).
    pub modules_bin: Option<PathBuf>,
    pub inner: Mutex<Inner>,
    install_lock: tokio::sync::Mutex<()>,
    pub catalog: catalog::Cache,
}

pub fn arch() -> &'static str {
    if cfg!(target_arch = "arm") { "armv7" } else if cfg!(target_arch = "aarch64") { "arm64" } else { std::env::consts::ARCH }
}

impl Inner {
    pub fn state_mut(&mut self, id: &str) -> &mut PluginState {
        self.state.entry(id.to_string()).or_default()
    }

    /// Значения настроек плагина с умолчаниями из манифеста.
    pub fn settings_of(&mut self, id: &str) -> Map<String, Value> {
        let mut out = Map::new();
        if let Some(p) = self.plugins.get(id) {
            for s in &p.manifest.settings {
                if let Some(d) = &s.default {
                    out.insert(s.key.clone(), d.clone());
                }
            }
        }
        for (k, v) in &self.state_mut(id).settings {
            out.insert(k.clone(), v.clone());
        }
        out
    }
}

impl Host {
    pub async fn install_module(self:&Arc<Self>,id:&str)->Result<Manifest,String> {
        let raw=modules::archive(id)?;
        self.install(&raw,"core",true,false).await
    }
    pub fn new(data_dir: PathBuf, hooks: Hooks, modules_bin: Option<PathBuf>) -> Arc<Host> {
        Arc::new(Host {
            data_dir,
            hooks,
            modules_bin,
            inner: Mutex::default(),
            install_lock: tokio::sync::Mutex::new(()),
            catalog: catalog::Cache::default(),
        })
    }

    pub fn dir(&self) -> PathBuf {
        self.data_dir.join("plugins")
    }

    fn state_path(&self) -> PathBuf {
        self.data_dir.join("plugins.json")
    }

    pub fn save(&self, inner: &Inner) {
        if let Ok(b) = serde_json::to_vec_pretty(&inner.state) {
            let _ = crate::settings::write_atomic(&self.state_path(), &b, 0o600);
        }
    }

    pub fn official(&self, id: &str) -> bool {
        self.inner.lock().unwrap().state.get(id).is_some_and(|s| s.official)
    }

    /// Прочитать установленные плагины и запустить включённые.
    pub async fn load(self: &Arc<Self>) {
        let state: BTreeMap<String, Option<PluginState>> =
            std::fs::read(self.state_path()).ok().and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or_default();
        let mut to_start = vec![];
        {
            let mut g = self.inner.lock().unwrap();
            for (id, st) in state {
                if let Some(st) = st {
                    g.state.insert(id, st);
                }
            }
            let mut dirs: Vec<_> = std::fs::read_dir(self.dir()).map(|r| r.flatten().collect()).unwrap_or_default();
            dirs.sort_by_key(|e| e.file_name());
            for e in dirs {
                let name = e.file_name().to_string_lossy().to_string();
                if !e.path().is_dir() || name.ends_with(".tmp") || name.ends_with(".old") {
                    continue;
                }
                let dir = e.path();
                let (m, broken) = match read_manifest(&dir) {
                    Ok(m) if m.id == name => (m, String::new()),
                    res => {
                        let why = match &res {
                            Ok(m) => format!("id в plugin.json ({}) не совпадает с папкой", m.id),
                            Err(e) => e.clone(),
                        };
                        if !valid_id(&name) {
                            log::info!("плагины: папка {name} пропущена: {why}");
                            continue;
                        }
                        log::info!("плагины: {name}: плагин повреждён: {why}");
                        let old = res.ok().unwrap_or_default();
                        let nm = if old.name.trim().is_empty() { name.clone() } else { old.name };
                        (
                            Manifest { id: name.clone(), name: nm, version: old.version, description: old.description, kind: old.kind, category: "other".into(), ..Default::default() },
                            format!("плагин повреждён: {why}"),
                        )
                    }
                };
                let later = broken.is_empty() && m.needs_newer_core();
                let err = if later { format!("заработает после обновления Джарвиса до {}", m.min_core) } else { broken.clone() };
                let enabled = g.state_mut(&m.id).enabled;
                if enabled && !later && broken.is_empty() {
                    to_start.push(m.id.clone());
                }
                g.plugins.insert(m.id.clone(), Plugin { manifest: m, dir, rt: None, err });
            }
            let ids: Vec<String> = g.state.keys().cloned().collect();
            for id in ids {
                // папку плагина удалили — забыть (кроме отметки «удалён руками»)
                if !g.plugins.contains_key(&id) && !g.state[&id].removed {
                    g.state.remove(&id);
                }
            }
            self.save(&g);
        }
        for id in to_start {
            self.start(&id).await;
        }
    }

    pub async fn start(self: &Arc<Self>, id: &str) {
        let (m, dir, settings, official) = {
            let mut g = self.inner.lock().unwrap();
            let Some(p) = g.plugins.get(id) else { return };
            let (m, dir) = (p.manifest.clone(), p.dir.clone());
            if m.needs_newer_core() {
                g.plugins.get_mut(id).unwrap().err = format!("заработает после обновления Джарвиса до {}", m.min_core);
                return;
            }
            let settings = g.settings_of(id);
            let official = g.state_mut(id).official;
            (m, dir, settings, official)
        };
        let res: Result<Runtime, String> = match m.kind.as_str() {
            "native" => native::Native::start(self.clone(), m.clone(), dir, settings, official).await.map(Runtime::Native),
            "module" => modules::Module::start(&m,Arc::downgrade(self)).map(|m|Runtime::Module(Arc::new(m))),
            "js" => Err("JS-плагины в новом ядре заменяются на Lua — плагин ждёт перевода".into()),
            #[cfg(feature = "lua")]
            "lua" => lua::Script::start(self.clone(), m.clone(), dir, settings).await.map(Runtime::Lua),
            #[cfg(not(feature = "lua"))]
            "lua" => Err("Эта сборка не включает Lua: включите модуль lua при сборке".into()),
            k => Err(format!("неизвестный вид плагина {k}")),
        };
        let mut g = self.inner.lock().unwrap();
        let Some(p) = g.plugins.get_mut(id) else { return };
        match res {
            Ok(rt) => {
                p.rt = Some(rt);
                p.err.clear();
                log::info!("плагины: {id} {} запущен", m.version);
            }
            Err(e) => {
                p.rt = None;
                log::info!("плагины: {id} не запустился: {e}");
                p.err = e;
            }
        }
    }

    pub async fn stop(&self, id: &str) {
        let rt = self.inner.lock().unwrap().plugins.get_mut(id).and_then(|p| p.rt.take());
        if let Some(rt) = rt {
            rt.stop().await;
        }
    }

    pub async fn stop_all(&self) {
        let rts: Vec<Runtime> = self.inner.lock().unwrap().plugins.values_mut().filter_map(|p| p.rt.take()).collect();
        for rt in rts {
            rt.stop().await;
        }
    }

    /// Запущенные плагины по алфавиту id (при одинаковых именах инструментов побеждает первый).
    pub fn running(&self) -> Vec<Runtime> {
        self.inner.lock().unwrap().plugins.values().filter_map(|p| p.rt.clone()).collect()
    }

    pub fn wake_training(&self) -> bool {
        let g = self.inner.lock().unwrap();
        g.state.get("training").is_some_and(|s| s.enabled)
            && g.plugins.get("training").is_some_and(|p| {
                p.manifest.provides("training") && match &p.rt {
                    Some(Runtime::Module(_)) => true,
                    Some(Runtime::Native(native)) => native.serves_http(),
                    _ => false,
                }
            })
    }

    pub fn tools(&self) -> Vec<Value> {
        let mut seen = std::collections::HashSet::new();
        let mut out = vec![];
        for rt in self.running() {
            for t in rt.tools() {
                let name = t.get("name").and_then(Value::as_str).unwrap_or("").to_string();
                if !name.is_empty() && seen.insert(name) {
                    out.push(t);
                }
            }
        }
        out
    }

    pub async fn call_tool(&self, name: &str, args: &str) -> Option<String> {
        for rt in self.running() {
            if let Some(r) = rt.call_tool(name, args).await {
                return Some(r);
            }
        }
        None
    }

    /// Быстрые команды плагинов: Some(ответ) — фраза разобрана без модели.
    pub async fn command(&self, text: &str) -> Option<String> {
        for rt in self.running() {
            if let Some(a) = rt.command(text).await {
                return Some(a);
            }
        }
        None
    }

    /// Запущенный провайдер cap: отдельный процесс или выбранный встроенный Rust-модуль.
    pub fn provider(&self, cap: &str) -> Result<Runtime, String> {
        self.provider_named(cap).map(|(_,runtime)|runtime)
    }
    fn provider_named(&self,cap:&str)->Result<(String,Runtime),String> {
        let g = self.inner.lock().unwrap();
        // Выбранный отдельный модуль владеет подключением и после выключения:
        // старый объединённый плагин не должен незаметно включать его обратно.
        let dedicated = g.plugins.contains_key(cap) || g.state.get(cap).is_some_and(|s|s.removed);
        let mut found = None;
        let mut rank = None;
        for p in g.plugins.values() {
            if dedicated && p.manifest.id != cap {continue;}
            if !p.manifest.provides(cap) {
                continue;
            }
            if let Some(rt) = p.rt.as_ref().filter(|r|matches!(r,Runtime::Native(_) | Runtime::Module(_))) {
                let current=(p.manifest.id==cap,p.manifest.kind=="module",g.state.get(&p.manifest.id).is_some_and(|s|s.official));
                if rank.is_none_or(|r|current>r) {
                    rank=Some(current);
                    found = Some((p.manifest.id.clone(),rt.clone()));
                }
            }
        }
        found.ok_or_else(|| format!("нет запущенного плагина «{cap}»"))
    }
    /// Панели нужен выбранный провайдер, включая намеренно выключенный отдельный модуль.
    pub fn service_providers(&self)->Value {
        let mut out=Map::new();
        for (service,cap) in [("yandex","yandexhome"),("homeassistant","homeassistant"),("xiaomi","xiaomi"),("smartlife","smartlife")] {
            let selected=self.provider_named(cap).ok().map(|(id,_)|id);
            let available=selected.is_some();
            let plugin=selected.unwrap_or_else(||{let g=self.inner.lock().unwrap();if g.plugins.contains_key(cap) || g.state.get(cap).is_some_and(|s|s.removed) {cap.into()}else{"home".into()}});
            out.insert(service.into(),json!({"plugin":plugin,"available":available}));
        }
        Value::Object(out)
    }

    pub async fn set_enabled(self: &Arc<Self>, id: &str, on: bool) -> Result<(), String> {
        let was = {
            let mut g = self.inner.lock().unwrap();
            if !g.plugins.contains_key(id) {
                return Err("нет такого плагина".into());
            }
            let st = g.state_mut(id);
            let was = st.enabled;
            st.enabled = on;
            self.save(&g);
            was
        };
        if on && !was {
            self.start(id).await;
        }
        if !on && was {
            self.stop(id).await;
        }
        Ok(())
    }

    /// Сохранить настройки плагина. Пустой пароль — оставить прежний (панель не видит ключи целиком).
    pub fn set_settings(&self, id: &str, input: &Map<String, Value>) -> Result<(), String> {
        let (settings, rt) = {
            let mut g = self.inner.lock().unwrap();
            let Some(p) = g.plugins.get(id) else { return Err("нет такого плагина".into()) };
            let defs = p.manifest.settings.clone();
            let st = g.state_mut(id);
            for s in &defs {
                let Some(v) = input.get(&s.key) else { continue };
                if s.kind == "password" && v.as_str().is_some_and(str::is_empty) {
                    continue;
                }
                if s.kind == "select" && !s.options.is_empty() && !s.options.iter().any(|o| Some(o.as_str()) == v.as_str()) {
                    return Err(format!("«{}»: нет такого варианта", s.label));
                }
                st.settings.insert(s.key.clone(), v.clone());
            }
            self.save(&g);
            let rt = g.plugins.get(id).and_then(|p| p.rt.clone());
            (g.settings_of(id), rt)
        };
        if let Some(rt) = rt {
            rt.configure(settings);
        }
        Ok(())
    }

    pub async fn uninstall(&self, id: &str) -> Result<(), String> {
        let dir = self.inner.lock().unwrap().plugins.get(id).map(|p| p.dir.clone()).ok_or("нет такого плагина")?;
        self.stop(id).await;
        std::fs::remove_dir_all(&dir).map_err(|e| e.to_string())?;
        let mut g = self.inner.lock().unwrap();
        g.plugins.remove(id);
        match g.state.get(id).cloned() {
            Some(st) if st.official => {
                g.state.insert(id.into(), PluginState { removed: true, enabled: false, source: st.source, official: true, ..Default::default() });
            }
            _ => {
                g.state.remove(id);
            }
        }
        self.save(&g);
        log::info!("плагины: удалён {id}");
        Ok(())
    }

    /// Для панели: установленные плагины, их настройки (ключи — только хвостом) и состояние.
    pub fn list(&self) -> Vec<Value> {
        let mut rows = vec![];
        {
            let mut g = self.inner.lock().unwrap();
            let ids: Vec<String> = g.plugins.keys().cloned().collect();
            for id in ids {
                let mut vals = g.settings_of(&id);
                let st = g.state_mut(&id).clone();
                let p = &g.plugins[&id];
                for s in p.manifest.settings.iter().filter(|s| s.kind == "password") {
                    if let Some(v) = vals.get(&s.key).and_then(Value::as_str).filter(|v| !v.is_empty()) {
                        let tail = if v.len() <= 8 { String::new() } else { v[v.len() - 4..].to_string() };
                        vals.insert(s.key.clone(), format!("•••{tail}").into());
                    }
                }
                rows.push((p.manifest.clone(), st, p.rt.clone(), p.err.clone(), p.dir.clone(), vals));
            }
        }
        let mut out: Vec<Value> = rows
            .into_iter()
            .map(|(m, st, rt, err, dir, vals)| {
                let mut v = serde_json::to_value(&m).unwrap_or(json!({}));
                let o = v.as_object_mut().unwrap();
                o.insert("enabled".into(), st.enabled.into());
                o.insert("running".into(), rt.is_some().into());
                o.insert("official".into(), st.official.into());
                o.insert("source".into(), st.source.into());
                if !err.is_empty() {
                    o.insert("error".into(), err.into());
                }
                if let Some(s) = rt.map(|r| r.status()).filter(|s| !s.is_empty()) {
                    o.insert("status".into(), s.into());
                }
                o.insert("values".into(), Value::Object(vals));
                o.insert("size".into(), dir_size(&dir).into());
                v
            })
            .collect();
        out.sort_by(|a, b| a["name"].as_str().cmp(&b["name"].as_str()));
        out
    }

    /// Коротко для /api/state: какие плагины стоят и включены.
    pub fn summary(&self) -> Value {
        let g = self.inner.lock().unwrap();
        let inst: Vec<&String> = g.plugins.keys().collect();
        let on: Vec<&String> = g.plugins.keys().filter(|id| g.state.get(*id).is_some_and(|s| s.enabled)).collect();
        json!({"installed": inst, "enabled": on})
    }

    /// Страницы плагинов в меню панели.
    pub fn pages(&self) -> Value {
        let g = self.inner.lock().unwrap();
        let mut out = vec![];
        for p in g.plugins.values() {
            let on = g.state.get(&p.manifest.id).is_some_and(|s| s.enabled);
            if !on || p.manifest.kind != "native" || p.rt.as_ref().and_then(Runtime::native).is_none() {
                continue;
            }
            for pg in p.manifest.pages.iter().filter(|pg| !pg.id.is_empty() && !pg.id.contains(['/', '.', '\\'])) {
                out.push(json!({"plugin": p.manifest.id, "id": pg.id, "title": pg.title, "icon": pg.icon, "group": pg.group}));
            }
        }
        Value::Array(out)
    }
}

pub fn read_manifest(dir: &Path) -> Result<Manifest, String> {
    let b = std::fs::read(dir.join("plugin.json")).map_err(|e| e.to_string())?;
    let m: Manifest = serde_json::from_slice(&b).map_err(|e| format!("plugin.json: {e}"))?;
    m.validate()?;
    Ok(m)
}

pub fn dir_size(dir: &Path) -> u64 {
    let mut n = 0;
    if let Ok(rd) = std::fs::read_dir(dir) {
        for e in rd.flatten() {
            match e.file_type() {
                Ok(t) if t.is_dir() => n += dir_size(&e.path()),
                Ok(_) => n += e.metadata().map(|m| m.len()).unwrap_or(0),
                _ => {}
            }
        }
    }
    n
}

pub fn clip(s: &str, n: usize) -> String {
    if s.chars().count() <= n { s.to_string() } else { s.chars().take(n).collect::<String>() + "…" }
}

pub const SHUTDOWN_WAIT: Duration = Duration::from_secs(2);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ids_and_versions() {
        assert!(valid_id("vpn") && valid_id("my-plugin2") && !valid_id("A") && !valid_id("-x") && !valid_id("x"));
        assert!(newer("0.9.10", "0.9.9") && !newer("0.9.71", "0.9.71-rs") && newer("v1.0.0", "0.9.99"));
        assert!(!valid_tool_name("smart_home") && valid_tool_name("coin_flip"));
    }
}
