use super::*;
use std::{collections::BTreeMap, io::Read};
pub struct Timer {
    at: Instant,
    period: Option<Duration>,
    function: Function,
}
pub type Timers = Arc<Mutex<BTreeMap<u64, Timer>>>;
pub fn due(timers: &Timers) -> Vec<(u64, Function)> {
    let mut timers = timers.lock().unwrap();
    let now = Instant::now();
    let keys: Vec<_> = timers
        .iter()
        .filter(|(_, t)| t.at <= now)
        .map(|(id, _)| *id)
        .collect();
    keys.into_iter()
        .filter_map(|id| {
            let t = timers.get_mut(&id)?;
            let f = t.function.clone();
            if let Some(period) = t.period {
                t.at = now + period;
            } else {
                timers.remove(&id);
            }
            Some((id, f))
        })
        .collect()
}
fn fail(error: impl std::fmt::Display) -> mlua::Error {
    mlua::Error::external(error.to_string())
}
fn permitted(url: &reqwest::Url, allowed: &[String]) -> bool {
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return false;
    }
    let Some(host) = url.host_str() else {
        return false;
    };
    allowed.iter().any(|a| {
        a == host
            || a == &url.origin().ascii_serialization()
            || a.strip_prefix("*.")
                .is_some_and(|suffix| host.ends_with(&format!(".{suffix}")))
    })
}
pub fn install(
    lua: &Lua,
    api: &Table,
    m: &Manifest,
    host: Weak<Host>,
    stopped: Arc<AtomicBool>,
    runtime: tokio::runtime::Handle,
    cpu: Arc<Mutex<Instant>>,
    wall: Arc<Mutex<Instant>>,
) -> mlua::Result<Timers> {
    api.set(
        "now",
        lua.create_function(|_, ()| Ok(jiff::Timestamp::now().as_millisecond()))?,
    )?;
    api.set(
        "normalize",
        lua.create_function(|_, s: String| Ok(super::super::command_text(&s)))?,
    )?;
    api.set(
        "random",
        lua.create_function(|_, (min, max): (i64, i64)| {
            let span = u64::try_from(max as i128 - min as i128 + 1)
                .map_err(|_| fail("неверный диапазон"))?;
            if span == 0 {
                return Err(fail("неверный диапазон"));
            }
            let limit = u64::MAX - u64::MAX % span;
            loop {
                let mut bytes = [0; 8];
                getrandom::fill(&mut bytes).map_err(fail)?;
                let n = u64::from_le_bytes(bytes);
                if n < limit {
                    return Ok((min as i128 + (n % span) as i128) as i64);
                }
            }
        })?,
    )?;
    api.set(
        "urlencode",
        lua.create_function(|_, text: String| {
            let mut out = String::new();
            for byte in text.bytes() {
                if byte.is_ascii_alphanumeric() || b"-_.~".contains(&byte) {
                    out.push(byte as char);
                } else {
                    out.push_str(&format!("%{byte:02X}"));
                }
            }
            Ok(out)
        })?,
    )?;
    if m.permissions.iter().any(|p| p == "storage") {
        let dir = host
            .upgrade()
            .ok_or_else(|| fail("хост завершился"))?
            .data_dir
            .join("plugins-data");
        std::fs::create_dir_all(&dir).map_err(fail)?;
        let path = dir.join(format!("{}.json", m.id));
        let store = lua.create_table()?;
        let p = path.clone();
        let stop = stopped.clone();
        store.set(
            "get",
            lua.create_function(move |lua, key: String| {
                if stop.load(Ordering::Relaxed) {
                    return Err(fail("плагин остановлен"));
                }
                let _lock = STORE_LOCK.lock().unwrap();
                let data = read_store(&p)?;
                match data.get(&key) {
                    Some(value) => lua.to_value(value),
                    None => Ok(mlua::Value::Nil),
                }
            })?,
        )?;
        let stop = stopped.clone();
        store.set(
            "set",
            lua.create_function(move |lua, (key, value): (String, mlua::Value)| {
                if stop.load(Ordering::Relaxed) {
                    return Err(fail("плагин остановлен"));
                }
                if key.is_empty() || key.len() > 256 {
                    return Err(fail("ключ хранилища: 1–256 байт"));
                }
                let value: Value = lua.from_value(value)?;
                let _lock = STORE_LOCK.lock().unwrap();
                let mut data = read_store(&path)?;
                if value.is_null() {
                    data.remove(&key);
                } else {
                    data.insert(key, value);
                }
                let bytes = serde_json::to_vec(&data).map_err(fail)?;
                if bytes.len() > 256 * 1024 {
                    return Err(fail("хранилище больше 256 КБ"));
                }
                crate::settings::write_atomic(&path, &bytes, 0o600).map_err(fail)
            })?,
        )?;
        api.set("store", store)?;
    }
    if m.permissions.iter().any(|p| p == "network") {
        let allowed = m.network_allow.clone();
        let stop = stopped.clone();
        api.set("fetch",lua.create_function(move |lua,(url,options):(String,Option<Table>)|{
            let started=Instant::now();let until=*wall.lock().unwrap();
            let mut url=reqwest::Url::parse(&url).map_err(|_|fail("неверный URL"))?;
            if !permitted(&url,&allowed){return Err(fail("домен не разрешён в network_allow"));}
            let options=options.map(|t|lua.from_value::<Value>(mlua::Value::Table(t))).transpose()?.unwrap_or(json!({}));
            let method=options["method"].as_str().unwrap_or("GET");let method=reqwest::Method::from_bytes(method.as_bytes()).map_err(|_|fail("неверный HTTP метод"))?;
            let body=options["body"].as_str().unwrap_or("").to_string();if body.len()>1024*1024{return Err(fail("HTTP body больше 1 МБ"));}
            let headers: BTreeMap<String,String>=options.get("headers").filter(|v|!v.is_null()).map(|v|serde_json::from_value(v.clone())).transpose().map_err(fail)?.unwrap_or_default();
            let route=(host.upgrade().ok_or_else(||fail("хост завершился"))?.hooks.network)();
            let result=runtime.block_on(async {
                let operation=async {
                    if let Some(error)=route["error"].as_str(){return Err(fail(error));}
                    let mut builder=reqwest::Client::builder().no_proxy().redirect(reqwest::redirect::Policy::none()).connect_timeout(Duration::from_secs(8)).timeout(Duration::from_secs(15));
                    if let Some(socks)=route["socks"].as_str() {
                        let address:std::net::SocketAddr=socks.parse().map_err(|_|fail("неверный SOCKS VPN"))?;
                        if !address.ip().is_loopback() || address.port()==0{return Err(fail("неверный SOCKS VPN"));}
                        builder=builder.proxy(reqwest::Proxy::all(format!("socks5h://{socks}")).map_err(fail)?.basic_auth(route["user"].as_str().unwrap_or(""),route["pass"].as_str().unwrap_or("")));
                    }
                    let client=builder.build().map_err(fail)?;
                    for redirect in 0..=5 {
                        if !permitted(&url,&allowed){return Err(fail("переход на запрещённый домен"));}
                        let mut request=client.request(method.clone(),url.clone()).body(body.clone());
                        for (key,value) in &headers {request=request.header(key,value);}
                        let mut response=request.send().await.map_err(|e|fail(crate::net::short(&e)))?;
                        if response.status().is_redirection() {
                            if redirect==5 || !matches!(method,reqwest::Method::GET|reqwest::Method::HEAD){return Err(fail("HTTP redirect отклонён"));}
                            let next=response.headers().get(reqwest::header::LOCATION).and_then(|v|v.to_str().ok()).ok_or_else(||fail("redirect без Location"))?;
                            let next=url.join(next).map_err(fail)?;
                            if url.scheme()=="https" && next.scheme()!="https" {return Err(fail("небезопасный HTTP redirect"));}
                            // Credentials cannot cross origins, even if both hosts are allowed.
                            if next.origin()!=url.origin() && !headers.is_empty(){return Err(fail("redirect с заголовками на другой домен"));}
                            url=next;continue;
                        }
                        let status=response.status().as_u16();let mut bytes=Vec::new();
                        while let Some(chunk)=response.chunk().await.map_err(|e|fail(crate::net::short(&e)))? {
                            if bytes.len()+chunk.len()>1024*1024{return Err(fail("HTTP ответ больше 1 МБ"));}bytes.extend_from_slice(&chunk);
                        }
                        return Ok(json!({"status":status,"ok":(200..300).contains(&status),"body":String::from_utf8(bytes).map_err(fail)?}));
                    }
                    Err(fail("слишком много redirect"))
                };
                tokio::select! {
                    value=operation=>value,
                    _=async {loop {if stop.load(Ordering::Relaxed) || Instant::now()>=until {break;}tokio::time::sleep(Duration::from_millis(25)).await;}}=>Err(fail("HTTP прерван или превышено время")),
                }
            });
            *cpu.lock().unwrap()+=started.elapsed();
            lua.to_value(&result?)
        })?)?;
    }
    let timers: Timers = Arc::default();
    if m.permissions.iter().any(|p| p == "timers") {
        let table = lua.create_table()?;
        let next = Arc::new(std::sync::atomic::AtomicU64::new(1));
        for (name, repeating) in [("after", false), ("every", true)] {
            let timers = timers.clone();
            let next = next.clone();
            let stop = stopped.clone();
            table.set(
                name,
                lua.create_function(move |_, (ms, function): (u64, Function)| {
                    if stop.load(Ordering::Relaxed) {
                        return Err(fail("плагин остановлен"));
                    }
                    if ms > 7 * 24 * 3600 * 1000 || (repeating && ms < 100) {
                        return Err(fail("неверный период таймера"));
                    }
                    let mut timers = timers.lock().unwrap();
                    if timers.len() >= 64 {
                        return Err(fail("лимит: 64 таймера"));
                    }
                    let id = next.fetch_add(1, Ordering::Relaxed);
                    let delay = Duration::from_millis(ms);
                    timers.insert(
                        id,
                        Timer {
                            at: Instant::now() + delay,
                            period: repeating.then_some(delay),
                            function,
                        },
                    );
                    Ok(id)
                })?,
            )?;
        }
        let timers = timers.clone();
        table.set(
            "cancel",
            lua.create_function(move |_, id: u64| {
                Ok(timers.lock().unwrap().remove(&id).is_some())
            })?,
        )?;
        api.set("timers", table)?;
    }
    Ok(timers)
}
static STORE_LOCK: Mutex<()> = Mutex::new(());
fn read_store(path: &std::path::Path) -> mlua::Result<Map<String, Value>> {
    let file = match std::fs::File::open(path) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Map::new()),
        Err(e) => return Err(fail(e)),
    };
    let mut bytes = Vec::new();
    file.take(256 * 1024 + 1)
        .read_to_end(&mut bytes)
        .map_err(fail)?;
    if bytes.len() > 256 * 1024 {
        return Err(fail("хранилище больше 256 КБ"));
    }
    serde_json::from_slice(&bytes).map_err(fail)
}
