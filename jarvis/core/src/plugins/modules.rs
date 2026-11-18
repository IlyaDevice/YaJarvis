//! Встроенные Rust-модули. Манифест включает модуль, Cargo feature выбирает его код и зависимости.
use super::Manifest;
use serde_json::Value;
use std::{
    sync::atomic::{AtomicBool, Ordering},
    time::Duration,
};

pub fn manifests() -> Vec<Manifest> {
    let mut out: Vec<Manifest> = Vec::new();
    #[cfg(feature="chatgpt")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/chatgpt/plugin.json")).expect("manifest chatgpt"));
    #[cfg(feature="openai")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/openai/plugin.json")).expect("manifest openai"));
    #[cfg(feature="gemini")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/gemini/plugin.json")).expect("manifest gemini"));
    #[cfg(feature="grok")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/grok/plugin.json")).expect("manifest grok"));
    #[cfg(feature="elevenlabs")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/elevenlabs/plugin.json")).expect("manifest elevenlabs"));
    #[cfg(feature = "homeassistant")]
    out.push(
        serde_json::from_str(include_str!(
            "../../../../plugins/homeassistant/plugin.json"
        ))
        .expect("manifest homeassistant"),
    );
    #[cfg(feature = "yandexhome")]
    out.push(
        serde_json::from_str(include_str!("../../../../plugins/yandexhome/plugin.json"))
            .expect("manifest yandexhome"),
    );
    #[cfg(feature = "smartlife")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/smartlife/plugin.json")).expect("manifest smartlife"));
    #[cfg(feature = "xiaomi")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/xiaomi/plugin.json")).expect("manifest xiaomi"));
    #[cfg(feature = "vpn")]
    out.push(
        serde_json::from_str(include_str!("../../../../plugins/vpn-rust/plugin.json"))
            .expect("manifest vpn"),
    );
    #[cfg(feature = "youtube")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/youtube/plugin.json")).expect("manifest youtube"));
    #[cfg(feature = "wake-training")]
    out.push(serde_json::from_str(include_str!("../../../../plugins/training/plugin.json")).expect("manifest training"));
    out.shrink_to_fit();
    out
}

pub fn archive(id: &str) -> Result<Vec<u8>, String> {
    use std::io::Write;
    let m = manifests()
        .into_iter()
        .find(|m| m.id == id)
        .ok_or("Этот модуль не включён в сборку")?;
    let mut zip = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
    zip.start_file("plugin.json", zip::write::SimpleFileOptions::default())
        .map_err(|e| e.to_string())?;
    zip.write_all(&serde_json::to_vec_pretty(&m).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    Ok(zip.finish().map_err(|e| e.to_string())?.into_inner())
}

pub struct Module {
    stopped: AtomicBool,
    cancel: tokio::sync::watch::Sender<bool>,
    implementation: Implementation,
    host: std::sync::Weak<super::Host>,
}
enum Implementation {
    #[cfg(feature="chatgpt")]
    ChatGpt(jarvis_chatgpt::ChatGpt),
    #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
    Cloud(jarvis_cloud::Provider),
    #[cfg(feature = "homeassistant")]
    HomeAssistant(jarvis_homeassistant::HomeAssistant),
    #[cfg(feature = "yandexhome")]
    YandexHome(jarvis_yandexhome::YandexHome),
    #[cfg(feature = "smartlife")]
    SmartLife(std::sync::Arc<jarvis_smartlife::SmartLife>),
    #[cfg(feature = "xiaomi")]
    Xiaomi(std::sync::Arc<jarvis_xiaomi::Xiaomi>),
    #[cfg(feature = "vpn")]
    Vpn(jarvis_vpn::Vpn),
    #[cfg(feature = "youtube")]
    Youtube(std::sync::Arc<jarvis_youtube::Youtube>),
    #[cfg(feature = "wake-training")]
    Training(std::sync::Arc<crate::training_records::Records>),
    #[cfg(not(any(feature = "homeassistant", feature = "yandexhome", feature = "smartlife", feature = "xiaomi", feature = "vpn",feature="openai",feature="gemini",feature="grok",feature="elevenlabs",feature="chatgpt",feature="youtube",feature="wake-training")))]
    Unavailable,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::plugins::{Hooks, Host};
    use serde_json::json;
    use std::sync::Arc;
    const MANIFEST: &str = include_str!("../../../../plugins/homeassistant/plugin.json");
    async fn install() -> (Arc<Host>, std::path::PathBuf) {
        use std::io::Write;
        let dir =
            std::env::temp_dir().join(format!("jarvis-module-{}", crate::settings::rand_hex(6)));
        std::fs::create_dir_all(&dir).unwrap();
        let mut zip = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
        zip.start_file("plugin.json", zip::write::SimpleFileOptions::default())
            .unwrap();
        zip.write_all(MANIFEST.as_bytes()).unwrap();
        let raw = zip.finish().unwrap().into_inner();
        let host = Host::new(dir.clone(), Hooks::default(), None);
        host.install(&raw, "file", false, false).await.unwrap();
        (host, dir)
    }
    fn cleanup(dir: std::path::PathBuf) {
        assert_eq!(dir.parent(), Some(std::env::temp_dir().as_path()));
        assert!(
            dir.file_name()
                .unwrap()
                .to_string_lossy()
                .starts_with("jarvis-module-")
        );
        std::fs::remove_dir_all(dir).unwrap();
    }
    #[cfg(feature = "yandexhome")]
    #[tokio::test]
    async fn yandex_module_uses_host_network_and_stops_without_native_process() {
        let dir =
            std::env::temp_dir().join(format!("jarvis-module-{}", crate::settings::rand_hex(6)));
        std::fs::create_dir_all(&dir).unwrap();
        let hooks = Hooks {
            request: Box::new(|id, method, _| {
                Box::pin(async move {
                    assert_eq!(id, "yandexhome");
                    assert_eq!(method, "network");
                    Ok(json!({"error":"VPN недоступен"}))
                })
            }),
            ..Default::default()
        };
        let host = Host::new(dir.clone(), hooks, None);
        host.install_module("yandexhome").await.unwrap();
        let provider = host.provider("yandexhome").unwrap();
        assert!(provider.native().is_none());
        assert_eq!(
            provider
                .request(
                    "yandexhome",
                    json!({"op":"has-catalog","token":"test-token"}),
                    Duration::from_secs(1)
                )
                .await
                .unwrap(),
            false
        );
        assert_eq!(
            provider
                .request(
                    "yandexhome",
                    json!({"op":"settings.validate","token":"test-token"}),
                    Duration::from_secs(1)
                )
                .await
                .unwrap_err(),
            "VPN недоступен"
        );
        host.set_enabled("yandexhome", false).await.unwrap();
        assert!(host.provider("yandexhome").is_err());
        assert!(
            provider
                .request(
                    "yandexhome",
                    json!({"op":"has-catalog"}),
                    Duration::from_secs(1)
                )
                .await
                .is_err()
        );
        host.set_enabled("yandexhome", true).await.unwrap();
        assert!(host.provider("yandexhome").is_ok());
        host.stop_all().await;
        cleanup(dir);
    }
    #[tokio::test]
    async fn module_zip_respects_compiled_features() {
        let (host, dir) = install().await;
        #[cfg(feature = "homeassistant")]
        {
            let provider = host.provider("homeassistant").unwrap();
            assert!(provider.native().is_none());
            assert_eq!(
                provider
                    .request(
                        "homeassistant",
                        json!({"op":"alive","config":{}}),
                        Duration::from_secs(1)
                    )
                    .await
                    .unwrap(),
                false
            );
            host.set_enabled("homeassistant", false).await.unwrap();
            assert!(host.provider("homeassistant").is_err());
            host.set_enabled("homeassistant", true).await.unwrap();
            assert!(host.provider("homeassistant").is_ok());
        }
        #[cfg(not(feature = "homeassistant"))]
        {
            assert!(host.provider("homeassistant").is_err());
            assert!(
                host.list()[0]["error"]
                    .as_str()
                    .unwrap()
                    .contains("не включён")
            );
        }
        host.stop_all().await;
        cleanup(dir);
    }
    #[cfg(feature = "wake-training")]
    #[tokio::test]
    async fn training_module_requires_installation_and_can_be_disabled() {
        let dir = std::env::temp_dir().join(format!("jarvis-module-{}", crate::settings::rand_hex(6)));
        std::fs::create_dir_all(&dir).unwrap();
        let host = Host::new(dir.clone(), Hooks::default(), None);
        assert!(!host.wake_training());
        host.install_module("training").await.unwrap();
        assert!(host.wake_training());
        host.set_enabled("training", false).await.unwrap();
        assert!(!host.wake_training());
        host.set_enabled("training", true).await.unwrap();
        assert!(host.wake_training());
        host.uninstall("training").await.unwrap();
        assert!(!host.wake_training());
        cleanup(dir);
    }
    #[cfg(feature = "homeassistant")]
    #[tokio::test]
    async fn disabling_module_cancels_in_flight_request() {
        let (host, dir) = install().await;
        let entered = Arc::new(tokio::sync::Notify::new());
        let signal = entered.clone();
        let app = axum::Router::new().route(
            "/api/states",
            axum::routing::get(move || {
                let signal = signal.clone();
                async move {
                    signal.notify_one();
                    std::future::pending::<String>().await
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        let provider = host.provider("homeassistant").unwrap();
        let request = tokio::spawn(async move {
            provider.request("homeassistant",json!({"op":"catalog","config":{"url":format!("http://{addr}"),"token":"test-token"},"args":true}),Duration::from_secs(30)).await
        });
        tokio::time::timeout(Duration::from_secs(3), entered.notified())
            .await
            .unwrap();
        host.set_enabled("homeassistant", false).await.unwrap();
        assert!(request.await.unwrap().unwrap_err().contains("остановлен"));
        server.abort();
        cleanup(dir);
    }
}
impl Module {
    #[cfg(any(feature="smartlife",feature="xiaomi"))]
    fn home_services(host:&std::sync::Weak<super::Host>,id:&'static str)->Result<jarvis_home_common::Host,String>{
        let data=host.upgrade().ok_or("Хост плагинов завершился")?.dir().join(id).join("data");
        let weak=host.clone();
        let services:jarvis_home_common::Callback=std::sync::Arc::new(move|method,params|{
            let weak=weak.clone();
            Box::pin(async move{
                let host=weak.upgrade().ok_or("Хост плагинов завершился")?;
                (host.hooks.request)(id.into(),method,params).await
            })
        });
        Ok(jarvis_home_common::Host::new(services,data))
    }
    #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
    pub async fn cloud_socket(&self,key:&str)->Result<(jarvis_cloud::Socket,tokio::sync::watch::Receiver<bool>),String>{
        self.cloud_socket_config(key,None).await
    }
    #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
    pub async fn cloud_socket_config(&self,key:&str,session:Option<Value>)->Result<(jarvis_cloud::Socket,tokio::sync::watch::Receiver<bool>),String>{
        if self.stopped.load(Ordering::SeqCst){return Err("модуль остановлен".into());}
        let mut cancel=self.cancel.subscribe();
        let Implementation::Cloud(provider)=&self.implementation else{return Err("Модуль не является голосовым провайдером".into());};
        let host=self.host.upgrade().ok_or("хост завершился")?;
        let network=(host.hooks.request)(provider.id().into(),"network".into(),serde_json::json!({})).await?;
        let socket=tokio::select!{
            _=cancel.changed()=>return Err("модуль остановлен".into()),
            result=tokio::time::timeout(Duration::from_secs(20),provider.socket_for(key,&network,session.as_ref()))=>result.map_err(|_|"Голосовое подключение не ответило".to_string())??,
        };
        Ok((socket,cancel))
    }
    pub fn start(m: &Manifest, host: std::sync::Weak<super::Host>) -> Result<Self, String> {
        let _ = &host;
        if m.id != m.main || m.provides != [m.main.clone()] {
            return Err(
                "Манифест модуля должен указывать его собственные id, main и provides".into(),
            );
        }
        match m.main.as_str() {
            #[cfg(feature = "wake-training")]
            "training" => {
                let root = host.upgrade().ok_or("Хост плагинов завершился")?.dir().join("training/data");
                Ok(Self {
                    host,
                    stopped: AtomicBool::new(false),
                    cancel: tokio::sync::watch::channel(false).0,
                    implementation: Implementation::Training(std::sync::Arc::new(crate::training_records::Records::new(root))),
                })
            }
            #[cfg(feature = "youtube")]
            "youtube" => {
                let weak = host.clone();
                let manifest = m.clone();
                let data = host.upgrade().ok_or("Хост плагинов завершился")?.dir().join("youtube/data");
                let services: jarvis_home_common::Callback = std::sync::Arc::new(move |method, params| {
                    let weak = weak.clone();
                    let manifest = manifest.clone();
                    Box::pin(async move {
                        let host = weak.upgrade().ok_or("Хост плагинов завершился")?;
                        if matches!(method.as_str(), "music.state" | "music.play" | "music.related_end") {
                            (host.hooks.event)(&manifest, &method, params);
                            return Ok(Value::Null);
                        }
                        (host.hooks.request)("youtube".into(), method, params).await
                    })
                });
                let service = std::sync::Arc::new(jarvis_youtube::Youtube::new(jarvis_home_common::Host::new(services, data)));
                service.watch();
                Ok(Self { host, stopped: AtomicBool::new(false), cancel: tokio::sync::watch::channel(false).0,
                    implementation: Implementation::Youtube(service) })
            }
            #[cfg(feature="smartlife")]
            "smartlife"=>{
                let provider=std::sync::Arc::new(jarvis_smartlife::SmartLife::new(Self::home_services(&host,"smartlife")?));
                provider.start_lan();
                Ok(Self{host,stopped:AtomicBool::new(false),cancel:tokio::sync::watch::channel(false).0,implementation:Implementation::SmartLife(provider)})
            }
            #[cfg(feature="xiaomi")]
            "xiaomi"=>{
                let provider=std::sync::Arc::new(jarvis_xiaomi::Xiaomi::new(Self::home_services(&host,"xiaomi")?)?);
                Ok(Self{host,stopped:AtomicBool::new(false),cancel:tokio::sync::watch::channel(false).0,implementation:Implementation::Xiaomi(provider)})
            }
            #[cfg(feature="chatgpt")]
            "chatgpt"=>{
                let weak=host.clone();let manifest=m.clone();let services:jarvis_chatgpt::Callback=std::sync::Arc::new(move|method,params|{let weak=weak.clone();let manifest=manifest.clone();Box::pin(async move{let host=weak.upgrade().ok_or("Хост завершился")?;if method=="voice.event"{(host.hooks.event)(&manifest,&method,params);return Ok(serde_json::json!(true));}(host.hooks.request)("chatgpt".into(),method,params).await})});
                Ok(Self{host,stopped:AtomicBool::new(false),cancel:tokio::sync::watch::channel(false).0,implementation:Implementation::ChatGpt(jarvis_chatgpt::ChatGpt::new(services))})
            },
            #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
            id if ["openai","gemini","grok","elevenlabs"].contains(&id)=>Ok(Self{
                host,stopped:AtomicBool::new(false),cancel:tokio::sync::watch::channel(false).0,
                implementation:Implementation::Cloud(jarvis_cloud::Provider::new(id)?),
            }),
            #[cfg(feature = "vpn")]
            "vpn" => Ok(Self {
                host,
                stopped: AtomicBool::new(false),
                cancel: tokio::sync::watch::channel(false).0,
                implementation: Implementation::Vpn(jarvis_vpn::Vpn::new()),
            }),
            #[cfg(feature = "homeassistant")]
            "homeassistant" => Ok(Self {
                host,
                stopped: AtomicBool::new(false),
                cancel: tokio::sync::watch::channel(false).0,
                implementation: Implementation::HomeAssistant(
                    jarvis_homeassistant::HomeAssistant::new()?,
                ),
            }),
            #[cfg(feature = "yandexhome")]
            "yandexhome" => Ok(Self {
                stopped: AtomicBool::new(false),
                cancel: tokio::sync::watch::channel(false).0,
                implementation: Implementation::YandexHome(jarvis_yandexhome::YandexHome::new()),
                host,
            }),
            _ => Err(format!(
                "Модуль «{}» не включён в эту сборку Джарвиса",
                m.main
            )),
        }
    }
    pub async fn request(
        &self,
        method: &str,
        params: Value,
        timeout: Duration,
    ) -> Result<Value, String> {
        if self.stopped.load(Ordering::SeqCst) {
            return Err("модуль остановлен".into());
        }
        let mut cancel = self.cancel.subscribe();
        if self.stopped.load(Ordering::SeqCst) {
            return Err("модуль остановлен".into());
        }
        tokio::select! {
            biased;
            _ = cancel.changed() => Err("модуль остановлен".into()),
            r = tokio::time::timeout(timeout, self.dispatch(method,params)) => r.map_err(|_|"модуль не ответил вовремя".to_string())?,
        }
    }
    async fn dispatch(&self, method: &str, params: Value) -> Result<Value, String> {
        match (&self.implementation, method) {
            #[cfg(feature = "youtube")]
            (Implementation::Youtube(service), "music") => service.music(params).await,
            #[cfg(feature = "youtube")]
            (Implementation::Youtube(service), "volume") => Ok(service.volume(&params)),
            #[cfg(feature="smartlife")]
            (Implementation::SmartLife(provider),"smartlife")=>provider.request(params).await,
            #[cfg(feature="xiaomi")]
            (Implementation::Xiaomi(provider),"xiaomi")=>provider.request(params).await,
            #[cfg(feature="chatgpt")]
            (Implementation::ChatGpt(provider),method)=>provider.request(method,params).await,
            #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
            (Implementation::Cloud(provider),method) if method==provider.id()=>{
                let host=self.host.upgrade().ok_or("хост завершился")?;
                let network=(host.hooks.request)(provider.id().into(),"network".into(),serde_json::json!({})).await?;
                provider.request(params,network).await
            },
            #[cfg(feature = "vpn")]
            (Implementation::Vpn(vpn), "vpn") => vpn.request(params).await,
            #[cfg(feature = "vpn")]
            (Implementation::Vpn(_), "vpn.validate") => jarvis_vpn::Vpn::validate(params),
            #[cfg(feature = "homeassistant")]
            (Implementation::HomeAssistant(ha), "homeassistant") => ha.request(params).await,
            #[cfg(feature = "yandexhome")]
            (Implementation::YandexHome(home), "yandexhome") => {
                let network = if ["has-catalog", "invalidate"]
                    .contains(&params["op"].as_str().unwrap_or(""))
                {
                    serde_json::json!({})
                } else {
                    let host = self.host.upgrade().ok_or("хост плагина завершился")?;
                    (host.hooks.request)(
                        "yandexhome".into(),
                        "network".into(),
                        serde_json::json!({}),
                    )
                    .await?
                };
                home.request(params, network).await
            }
            _ => {
                let _ = params;
                Err(format!("модуль не поддерживает метод {method}"))
            }
        }
    }
    #[cfg(feature = "wake-training")]
    pub fn records(&self) -> Option<std::sync::Arc<crate::training_records::Records>> {
        match &self.implementation { Implementation::Training(records) => Some(records.clone()), _ => None }
    }

    pub async fn stop(&self) {
        self.stopped.store(true, Ordering::SeqCst);
        self.cancel.send_replace(true);
        #[cfg(feature = "wake-training")]
        if let Implementation::Training(records) = &self.implementation { records.stop(); }
        #[cfg(feature = "youtube")]
        if let Implementation::Youtube(service) = &self.implementation { service.stop().await; }
        #[cfg(feature="smartlife")]
        if let Implementation::SmartLife(provider)=&self.implementation{provider.stop();}
        #[cfg(feature="xiaomi")]
        if let Implementation::Xiaomi(provider)=&self.implementation{provider.stop();}
        #[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
        if let Implementation::Cloud(provider)=&self.implementation{provider.close();}
        #[cfg(feature="chatgpt")]
        if let Implementation::ChatGpt(provider)=&self.implementation{provider.shutdown().await;}
        #[cfg(feature = "vpn")]
        if let Implementation::Vpn(vpn) = &self.implementation {
            vpn.stop().await;
        }
    }
}
