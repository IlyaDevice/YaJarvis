//! Хост плагинов против настоящего плагина на pluginsdk (Go): тот же протокол, что на колонке.
//! Нужен Go: JARVIS_GO или go в PATH; без него тест пропускается.

use super::*;
use std::process::Command as Std;

const DEMO: &str = r#"package main

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"time"

	"github.com/newfpv/YaJavis/pluginsdk"
)

func main() {
	p := pluginsdk.New()
	var contextStarted atomic.Int64
	contextBarrier := make(chan struct{})
	for _, service := range []string{"xiaomi", "smartlife"} {
		service := service
		p.HandleContext(service, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var req struct { Op string `json:"op"` }
			_ = json.Unmarshal(raw, &req)
			if req.Op == "devices" { return []any{map[string]any{"id":"test-device"}}, nil }
			if req.Op == "context" {
				if contextStarted.Add(1) == 2 { close(contextBarrier) }
				select {
				case <-contextBarrier: return service+" parallel", nil
				case <-ctx.Done(): return nil, ctx.Err()
				}
			}
			return nil, nil
		})
	}
	var homeReady atomic.Bool
	p.Handle("yandexhome", func(raw json.RawMessage) (any, error) {
		var req struct { Op string `json:"op"` }
		_ = json.Unmarshal(raw, &req)
		if req.Op == "context" { homeReady.Store(true); return "дом", nil }
		if req.Op == "has-catalog" { return homeReady.Load(), nil }
		return nil, nil
	})
	var pending atomic.Int64
	p.Handle("pending", func(json.RawMessage) (any, error) { return pending.Load(), nil })
	p.HandleContext("music", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var req struct { Op string `json:"op"` }
		_ = json.Unmarshal(raw, &req)
		if req.Op == "slow" || req.Op == "propose" { pending.Add(1); defer pending.Add(-1); <-ctx.Done(); return nil, ctx.Err() }
		state := map[string]any{"active": true, "playing": true, "status": "Тестовый трек"}
		if req.Op == "fail" { return map[string]any{"state": state, "error": "ошибка плеера"}, nil }
		return map[string]any{"state": state, "value": "готово"}, nil
	})
	p.Tool(pluginsdk.ToolDef{Name: "coin", Description: "монетка"}, func(args json.RawMessage) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out map[string]any
		if err := p.Core(ctx, "network", map[string]any{"service": "plugins"}, &out); err != nil {
			return "", err
		}
		b, _ := json.Marshal(out)
		return "орёл " + string(b), nil
	})
	p.Tool(pluginsdk.ToolDef{Name: "smart_home", Description: "чужое имя"}, func(json.RawMessage) (string, error) { return "", nil })
	p.Tool(pluginsdk.ToolDef{Name: "crash", Description: "упасть"}, func(json.RawMessage) (string, error) { os.Exit(3); return "", nil })
	p.Command(func(text string) (bool, string) { return text == "подбрось монетку", "Выпал орёл" })
	p.Run()
}
"#;

fn go_bin() -> Option<String> {
    let go = std::env::var("JARVIS_GO").unwrap_or_else(|_| "go".into());
    Std::new(&go).arg("version").output().ok().filter(|o| o.status.success()).map(|_| go)
}

fn build_demo(dir: &std::path::Path) -> Option<String> {
    let go = go_bin()?;
    let src = dir.join("src");
    std::fs::create_dir_all(&src).unwrap();
    let sdk = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../pluginsdk").canonicalize().ok()?;
    let sdk = sdk.to_string_lossy().trim_start_matches(r"\\?\").replace('\\', "/");
    std::fs::write(src.join("main.go"), DEMO).unwrap();
    std::fs::write(
        src.join("go.mod"),
        format!("module demo\n\ngo 1.24\n\nrequire github.com/newfpv/YaJavis/pluginsdk v0.0.0\n\nreplace github.com/newfpv/YaJavis/pluginsdk => {sdk}\n"),
    )
    .unwrap();
    let _ = std::fs::copy(std::path::Path::new(&sdk).join("go.sum"), src.join("go.sum"));
    let exe = if cfg!(windows) { ".exe" } else { "" };
    let name = format!("demo-{}{exe}", arch());
    let out = Std::new(&go).args(["build", "-mod=mod", "-o"]).arg(dir.join("plugins/demo").join(&name)).arg(".").current_dir(&src).output().ok()?;
    if !out.status.success() {
        panic!("сборка плагина: {}", String::from_utf8_lossy(&out.stderr));
    }
    Some(format!("demo-{{arch}}{exe}"))
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn native_plugin_protocol() {
    let dir = std::env::temp_dir().join(format!("jarvis-pl-{}", crate::settings::rand_hex(4)));
    std::fs::create_dir_all(dir.join("plugins/demo")).unwrap();
    let Some(main) = build_demo(&dir) else {
        eprintln!("нет Go — пропускаю");
        return;
    };
    std::fs::write(
        dir.join("plugins/demo/plugin.json"),
        format!(r#"{{"id":"demo","name":"Демо","version":"1.0.0","kind":"native","category":"fun","provides":["youtube","homeassistant","yandexhome","xiaomi","smartlife"],"main":"{main}"}}"#),
    )
    .unwrap();
    let hooks = Hooks {
        request: Box::new(|id, method, _| Box::pin(async move { Ok(json!({"who": id, "method": method})) })),
        ..Default::default()
    };
    let host = Host::new(dir.clone(), hooks, None);
    host.load().await;
    let settings=crate::settings::Store::open(dir.join("settings.json")).unwrap();
    settings.update(|s|->Result<(),std::io::Error>{s.yandex_token="test-token".into();s.xiaomi=json!({"user_id":"test","ssecurity":"test","service_token":"test"});s.tuya=json!({"endpoint":"test","refresh_token":"test"});s.home_use.insert("alice".into(),false);Ok(())}).unwrap();
    let tools=crate::tools::Tools::new(host.clone(),settings);
    let (context,defs)=tools.prepare().await;
    assert!(context.contains("дом"));
    assert!(context.contains("xiaomi parallel") && context.contains("smartlife parallel"),"провайдеры должны готовиться параллельно");
    assert!(defs.iter().any(|d|d["name"]=="xiaomi_home") && defs.iter().any(|d|d["name"]=="smart_life"));
    assert!(defs.iter().any(|d|d["name"]=="smart_home"),"в первой реплике инструмент должен видеть уже загруженный каталог");
    for module in compiled_modules().into_iter().filter(|m| matches!(m.as_str(), "homeassistant" | "yandexhome")) {
        let service=if module=="yandexhome" {"yandex"}else{module.as_str()};
        assert!(host.provider(&module).unwrap().native().is_some());
        assert_eq!(host.service_providers()[service]["plugin"],"demo");
        host.install_module(&module).await.unwrap();
        assert!(host.provider(&module).unwrap().native().is_none());
        assert_eq!(host.service_providers()[service]["plugin"],module);
        host.set_enabled(&module,false).await.unwrap();
        assert!(host.provider(&module).is_err(),"выключенный модуль не должен заменяться старым провайдером");
        assert_eq!(host.service_providers()[service]["available"],false);
        assert_eq!(host.service_providers()[service]["plugin"],module);
        host.uninstall(&module).await.unwrap();
        assert!(host.provider(&module).is_err(),"удалённый модуль тоже не должен незаметно заменяться");
    }
    let music = crate::music::Music::default();
    assert_eq!(music.call(&host,"play",json!({"query":"test"})).await.unwrap(),"готово");
    assert!(music.state().playing);
    assert_eq!(music.control(&host,"что играет").await.as_deref(),Some("Тестовый трек"));
    assert_eq!(music.call(&host,"fail",Value::Null).await.unwrap_err(),"ошибка плеера");
    assert!(music.state().active, "состояние из ответа с ошибкой тоже применяется");
    // Отмена future запроса не оставляет висящий pending у хоста.
    let provider = host.provider("youtube").unwrap();
    let result = tokio::time::timeout(Duration::from_millis(50),provider.request("music",json!({"op":"slow"}),Duration::from_secs(30))).await;
    assert!(result.is_err());
    let pending = music.play(&host,"мои рекомендации",false);
    tokio::pin!(pending);
    tokio::select! {
        result = &mut pending => panic!("операция должна ждать: {result}"),
        _ = tokio::time::sleep(Duration::from_millis(50)) => {},
    }
    assert_eq!(music.call(&host,"stop",Value::Null).await.unwrap(),"готово");
    assert!(pending.await.contains("операция отменена"));
    let mut left = 1;
    for _ in 0..50 {
        left = provider.request("pending",Value::Null,Duration::from_secs(1)).await.unwrap().as_i64().unwrap();
        if left == 0 {break;}
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert_eq!(left,0,"отмена должна дойти до выполняющейся операции модуля");
    let list = host.list();
    assert_eq!(list[0]["running"], true, "{list:?}");

    // register приходит уведомлением после init — ждём инструменты
    for _ in 0..50 {
        if !host.tools().is_empty() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    let names: Vec<String> = host.tools().iter().map(|t| t["name"].as_str().unwrap().to_string()).collect();
    assert!(names.contains(&"coin".to_string()) && !names.contains(&"smart_home".to_string()), "{names:?}");

    let r = host.call_tool("coin", "{}").await.unwrap();
    assert!(r.starts_with("орёл") && r.contains(r#""who":"demo""#) && r.contains(r#""method":"network""#), "{r}");
    assert_eq!(host.command("Джарвис, подбрось монетку!").await.as_deref(), Some("Выпал орёл"));
    assert_eq!(host.command("включи свет").await, None);
    assert_eq!(host.call_tool("nope", "{}").await, None);

    // упал — перезапускается сам (через 2 с) и снова отвечает
    let r = host.call_tool("crash", "{}").await.unwrap();
    assert!(r.contains("не справился"), "{r}");
    let mut back = false;
    for _ in 0..80 {
        tokio::time::sleep(Duration::from_millis(100)).await;
        if host.call_tool("coin", "{}").await.is_some_and(|r| r.starts_with("орёл")) {
            back = true;
            break;
        }
    }
    assert!(back, "плагин не перезапустился");

    host.set_enabled("demo", false).await.unwrap();
    assert_eq!(host.list()[0]["running"], false);
    host.uninstall("demo").await.unwrap();
    assert!(host.list().is_empty());
    let _ = std::fs::remove_dir_all(dir);
}
