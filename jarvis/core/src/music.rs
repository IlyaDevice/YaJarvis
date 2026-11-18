//! Общий интерфейс музыки. Поиск, аккаунт и декодирование принадлежат модулю-провайдеру.
use crate::{fastcmd, plugins::Host};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Remote {
    pub playing: bool,
    pub active: bool,
    pub paused: bool,
    pub proposal: bool,
    pub personal_search: bool,
    pub cur: Value,
    pub pos: f64,
    pub status: String,
}

pub struct Music {
    state: Mutex<Remote>,
    cancel: tokio::sync::watch::Sender<u64>,
}
impl Default for Music {
    fn default() -> Self {
        Self {
            state: Mutex::default(),
            cancel: tokio::sync::watch::channel(0).0,
        }
    }
}

impl Music {
    pub fn state(&self) -> Remote {
        self.state.lock().unwrap().clone()
    }

    pub fn event(&self, name: &str, data: Value) {
        match name {
            "music.state" => {
                if let Ok(st) = serde_json::from_value(data) {
                    *self.state.lock().unwrap() = st;
                }
            }
            "exit" => {
                self.cancel.send_modify(|n| *n = n.wrapping_add(1));
                *self.state.lock().unwrap() = Remote::default();
            }
            _ => {}
        }
    }

    pub async fn call(&self, host: &Host, op: &str, args: Value) -> Result<Value, String> {
        if op == "stop" {
            self.cancel.send_modify(|n| *n = n.wrapping_add(1));
        }
        let mut cancel = self.cancel.subscribe();
        let provider = host.provider("youtube").map_err(|_| {
            "Установите и включите модуль музыки YouTube на странице «Плагины».".to_string()
        })?;
        let timeout = if [
            "play",
            "play_id",
            "enqueue",
            "propose",
            "related",
            "resume_saved",
            "respond",
            "next",
        ]
        .contains(&op)
        {
            70
        } else if matches!(op, "cookies_set" | "account_begin") {
            30
        } else {
            10
        };
        let request = provider.request(
            "music",
            json!({"op": op, "args": args}),
            Duration::from_secs(timeout),
        );
        let cancellable = [
            "play",
            "play_id",
            "enqueue",
            "propose",
            "related",
            "resume_saved",
            "respond",
            "next",
            "respond",
        ]
        .contains(&op);
        let out = tokio::select! {
            biased;
            _ = cancel.changed(), if cancellable => return Err("операция отменена".into()),
            result = request => result?,
        };
        if let Some(st) = out.get("state").filter(|v| !v.is_null()) {
            let st =
                serde_json::from_value(st.clone()).map_err(|e| format!("состояние музыки: {e}"))?;
            *self.state.lock().unwrap() = st;
        }
        if let Some(err) = out
            .get("error")
            .and_then(Value::as_str)
            .filter(|e| !e.is_empty())
        {
            return Err(err.into());
        }
        Ok(out.get("value").cloned().unwrap_or(Value::Null))
    }

    pub async fn snapshot(&self, host: &Host) -> Value {
        match self.call(host, "snapshot", Value::Null).await {
            Ok(v) => v,
            Err(_) => {
                let s = self.state();
                json!({"source":"youtube", "playing":s.playing,"paused":s.paused,"track":s.cur,"pos":s.pos})
            }
        }
    }

    pub async fn play(&self, host: &Host, query: &str, queue: bool) -> String {
        let propose = recommendation(query);
        let result = self
            .call(
                host,
                if queue {
                    "enqueue"
                } else if propose {
                    "propose"
                } else {
                    "play"
                },
                json!({"query":query}),
            )
            .await;
        match result {
            Ok(v) if queue => format!("В очереди: {}.", title(&v)),
            Ok(Value::String(s)) if propose => format!(
                "{s} Ролик ещё не включён. Дождись ответа человека и передай его в music_control."
            ),
            Ok(v) => format!("Включил с YouTube: {}.", title(&v)),
            Err(e) => format!("Не получилось: {e}"),
        }
    }

    pub async fn control(&self, host: &Host, text: &str) -> Option<String> {
        if self.state().proposal {
            match self.call(host, "respond", json!({"text":text})).await {
                Ok(v) if v["ok"].as_bool() == Some(true) => {
                    return Some(v["answer"].as_str().unwrap_or("").into());
                }
                Err(e) => return Some(format!("Не получилось: {e}")),
                _ => {}
            }
        }
        if !self.state().active {
            return None;
        }
        let (op, args, answer) = command(text)?;
        if op == "status" {
            return Some(self.state().status);
        }
        Some(match self.call(host, op, args).await {
            Err(e) => format!("Не получилось: {e}"),
            Ok(Value::String(s)) if op == "related" => s,
            Ok(_) => answer.into(),
        })
    }
}

fn title(v: &Value) -> &str {
    v.get("Title")
        .or_else(|| v.get("title"))
        .and_then(Value::as_str)
        .unwrap_or("трек")
}

fn recommendation(text: &str) -> bool {
    let t = fastcmd::strip_lead(&fastcmd::norm_text(text));
    if [
        "рекомендац",
        "рекомендован",
        "что нибудь ",
        "подбери ",
        "выбери ",
        "предложи ",
    ]
    .iter()
    .any(|p| t.starts_with(p))
    {
        return true;
    }
    if ["мои рекомендации", "что то интересное"].contains(&t.as_str())
    {
        return true;
    }
    ["включи ", "поставь ", "запусти ", "давай ", "найди "]
        .iter()
        .any(|p| t.starts_with(p))
        && [
            "мои рекомендации",
            "моих рекомендац",
            "из рекомендац",
            "что нибудь",
            "что то интересное",
            "послушать",
            "чтобы болтало на фоне",
            "сам ",
            "самому",
            "на свой вкус",
        ]
        .iter()
        .any(|p| t.contains(p))
}

pub fn command(text: &str) -> Option<(&'static str, Value, &'static str)> {
    let lowered = text
        .to_lowercase()
        .replace('ё', "е")
        .replace([',', '.', '!', '?'], " ");
    let t = fastcmd::strip_lead(&lowered)
        .trim_matches(|c: char| c.is_whitespace() || ",.!?".contains(c))
        .trim_start_matches("пожалуйста ")
        .to_string();
    let starts = |s: &[&str]| s.iter().any(|p| t.starts_with(p));
    let simple = |op, answer| Some((op, Value::Null, answer));
    if starts(&[
        "что играет",
        "что сейчас играет",
        "что за пес",
        "что за трек",
        "кто поет",
        "как называется пес",
    ]) {
        return simple("status", "");
    }
    if starts(&["сначала", "с начала", "в начало", "включи сначала"])
    {
        return Some(("seek", json!({"delta":0,"abs":true}), "С начала."));
    }
    if starts(&[
        "перемот",
        "промот",
        "мотни",
        "вперед на",
        "назад на",
        "отмотай",
    ]) {
        let (delta, abs) = seek(&t);
        return Some(("seek", json!({"delta":delta,"abs":abs}), "Перемотал."));
    }
    if starts(&["продолж", "сними с пауз", "сними пауз", "играй дальше"])
    {
        return simple("resume", "Продолжаю.");
    }
    if starts(&[
        "пауза",
        "на паузу",
        "поставь на паузу",
        "останови музыку",
        "приостанов",
    ]) {
        return simple("pause", "Пауза.");
    }
    if starts(&["выключи музыку", "выключи песню", "выруби музыку", "стоп"])
    {
        return simple("stop", "Выключил.");
    }
    if starts(&[
        "следующ",
        "включи следующ",
        "поставь следующ",
        "другую песню",
        "другой трек",
        "переключи",
        "дальше",
        "включи похож",
        "поставь похож",
    ]) {
        return Some(("related", json!({"expected":""}), ""));
    }
    None
}

fn seek(t: &str) -> (f64, bool) {
    for word in t.split_whitespace() {
        if let Some((m, s)) = word.split_once(':') {
            if let (Ok(m), Ok(s)) = (m.parse::<u32>(), s.parse::<u32>()) {
                if s < 60 {
                    return ((m as f64) * 60.0 + s as f64, true);
                }
            }
        }
    }
    let words: Vec<_> = t.split_whitespace().collect();
    let mut total = 0.0;
    for (i, w) in words.iter().enumerate() {
        let n = match *w {
            "полминуты" => {
                total += 30.0;
                continue;
            }
            "минуту" if i == 0 || amount(words[i - 1]).is_none() => {
                total += 60.0;
                continue;
            }
            _ => amount(w),
        };
        if let Some(n) = n {
            let unit = words.get(i + 1).copied().unwrap_or("");
            total += n * if unit.starts_with("мин")
                || *w == "полторы"
                || (*w == "пару" && unit.is_empty())
            {
                60.0
            } else {
                1.0
            };
        }
    }
    if total == 0.0 {
        total = 30.0;
    }
    if t.contains("назад") || t.contains("отмотай") {
        total = -total;
    }
    (total, t.contains("на отметку") || t.contains("на позицию"))
}
fn amount(w: &str) -> Option<f64> {
    Some(match w {
        "одну" | "один" => 1.0,
        "два" | "две" | "пару" => 2.0,
        "три" => 3.0,
        "четыре" => 4.0,
        "пять" => 5.0,
        "десять" => 10.0,
        "пятнадцать" => 15.0,
        "двадцать" => 20.0,
        "тридцать" => 30.0,
        "сорок" => 40.0,
        "пятьдесят" => 50.0,
        "полторы" => 1.5,
        _ => w.parse().ok()?,
    })
}

pub fn volume_loop(music: Arc<Music>, host: Arc<Host>, duck: impl Fn() -> bool + Send + 'static) {
    tokio::spawn(async move {
        let mut last = -1.0;
        loop {
            tokio::time::sleep(Duration::from_millis(50)).await;
            if !music.state().playing {
                last = -1.0;
                continue;
            }
            let v = if duck() { 0.16 } else { 0.8 };
            if v == last {
                continue;
            }
            if let Ok(n) = host.provider("youtube") {
                if n.notify("volume", json!({"v":v})).await.is_ok() {
                    last = v;
                }
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn parses_controls_without_catching_questions() {
        assert!(command("расскажи как перемотать фильм").is_none());
        assert_eq!(
            command("Джарвис, перемотай назад на 2 минуты 15 секунд")
                .unwrap()
                .1,
            json!({"delta":-135.0,"abs":false})
        );
        assert_eq!(
            command("перемотай на 2:05").unwrap().1,
            json!({"delta":125.0,"abs":true})
        );
        assert_eq!(command("перемотай на полминуты").unwrap().1["delta"], 30.0);
        assert_eq!(command("пауза").unwrap().0, "pause");
    }
    #[test]
    fn resets_state_on_exit() {
        let m = Music::default();
        m.event("music.state", json!({"active":true,"playing":true}));
        assert!(m.state().playing);
        m.event("exit", Value::Null);
        assert!(!m.state().active);
    }
}
