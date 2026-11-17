//! VPN по подключениям: туннель держит плагин «VPN» (локальный SOCKS5 с паролем), ядро только знает,
//! куда ходить. Подключение с галочкой «через VPN» при недоступном туннеле НИКОГДА не идёт напрямую.

use crate::{plugins::Host, settings::Store};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    net::SocketAddr,
    sync::{Arc, RwLock},
    time::Duration,
};

pub const VPN_MISSING: &str = "VPN теперь отдельный плагин: поставьте «VPN» на странице «Плагины»";

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct Tunnel {
    pub socks: String,
    pub user: String,
    pub pass: String,
    pub info: Option<Value>,
}

impl Tunnel {
    fn valid(&self) -> bool {
        self.socks.parse::<SocketAddr>().is_ok_and(|a| a.ip().is_loopback() && a.port() > 0)
            && (1..=255).contains(&self.user.len())
            && (1..=255).contains(&self.pass.len())
    }
}

#[derive(Default)]
struct St {
    link: String,
    tun: Option<Tunnel>,
    err: String,
}

pub struct Proxy {
    st: RwLock<St>,
    store: Store,
}

/// Куда ходить подключению: напрямую, через туннель или никуда (с причиной).
pub enum Route {
    Direct,
    Tunnel(Tunnel),
    Blocked(String),
}

impl Proxy {
    pub fn new(store: Store) -> Arc<Proxy> {
        Arc::new(Proxy { st: RwLock::default(), store })
    }

    pub fn route(&self, svc: &str) -> Route {
        let use_vpn = svc.is_empty() || self.store.with(|s| crate::web::state::use_vpn(s, svc));
        let st = self.st.read().unwrap();
        if !use_vpn || st.link.is_empty() {
            return Route::Direct;
        }
        match &st.tun {
            Some(t) => Route::Tunnel(t.clone()),
            None => Route::Blocked(if st.err.is_empty() { "плагин VPN запускается".into() } else { st.err.clone() }),
        }
    }

    /// Ответ сервиса ядра «network» для плагина: {} — напрямую, {socks,user,pass} — через туннель, {error}.
    pub fn network_reply(&self, svc: &str) -> Value {
        match self.route(svc) {
            Route::Direct => json!({}),
            Route::Tunnel(t) => json!({"socks": t.socks, "user": t.user, "pass": t.pass}),
            Route::Blocked(why) => json!({"error": why}),
        }
    }

    pub fn status(&self) -> (Option<Value>, String) {
        let st = self.st.read().unwrap();
        (st.tun.as_ref().and_then(|t| t.info.clone()), st.err.clone())
    }

    /// Свериться со ссылкой из настроек и плагином VPN.
    pub async fn refresh(&self, host: &Host) {
        let link = self.store.with(|s| s.vless.trim().to_string());
        let (tun, err) = if link.is_empty() {
            (None, String::new())
        } else {
            match vpn_provider(host) {
                Err(why) => (None, why),
                Ok(n) => match n.request("vpn", json!({"link": link}), Duration::from_secs(5)).await {
                    Ok(v) => match serde_json::from_value::<Tunnel>(v) {
                        Ok(t) if t.valid() => (Some(t), String::new()),
                        _ => (None, "плагин VPN вернул неправильный локальный SOCKS".into()),
                    },
                    Err(e) => (None, e),
                },
            }
        };
        let mut st = self.st.write().unwrap();
        st.link = link;
        st.tun = tun;
        st.err = err;
    }

    pub async fn run(self: Arc<Self>, host: Arc<Host>) {
        loop {
            self.refresh(&host).await;
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
    }
}

/// Включённый плагин с provides "vpn"; официальный «vpn» побеждает.
fn vpn_provider(host: &Host) -> Result<Arc<crate::plugins::native::Native>, String> {
    let g = host.inner.lock().unwrap();
    let mut chosen: Option<&crate::plugins::Plugin> = None;
    let mut disabled = false;
    for p in g.plugins.values().filter(|p| p.manifest.provides("vpn")) {
        let st = g.state.get(&p.manifest.id);
        let on = st.is_some_and(|s| s.enabled && !s.removed);
        if !on {
            disabled |= st.is_some_and(|s| !s.removed);
            continue;
        }
        if chosen.is_none_or(|c| p.manifest.id == "vpn" || (c.manifest.id != "vpn" && p.manifest.id < c.manifest.id)) {
            chosen = Some(p);
        }
    }
    let Some(p) = chosen else {
        return Err(if disabled { "Плагин VPN выключен — включите его на странице «Плагины»".into() } else { VPN_MISSING.into() });
    };
    p.rt.as_ref().and_then(|r| r.native().cloned()).ok_or_else(|| "плагин VPN запускается".into())
}
