//! Хост плагинов против настоящего плагина на pluginsdk (Go): тот же протокол, что на колонке.
//! Нужен Go: JARVIS_GO или go в PATH; без него тест пропускается.

use super::*;
use std::process::Command as Std;

const DEMO: &str = r#"package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/newfpv/YaJavis/pluginsdk"
)

func main() {
	p := pluginsdk.New()
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
        format!(r#"{{"id":"demo","name":"Демо","version":"1.0.0","kind":"native","category":"fun","main":"{main}"}}"#),
    )
    .unwrap();
    let hooks = Hooks {
        request: Box::new(|id, method, _| Box::pin(async move { Ok(json!({"who": id, "method": method})) })),
        ..Default::default()
    };
    let host = Host::new(dir.clone(), hooks, None);
    host.load().await;
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
