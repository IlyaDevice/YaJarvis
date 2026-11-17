//! Штатная Алиса: команда ей через локальный канал Quasar (127.0.0.1:9875) — как будто сказали голосом;
//! журнал maind — «Алиса» позвали, кнопка пауза; перезапуск Алисы, если она поднялась без нашего vqe_tap.so.
//!
//! Кадр: длина u32 LE + protobuf; Remoting{7:"AliceCapability", 8:AliceCapabilityMethod}, 253=210.
//! AliceCapabilityMethod{1: START_REQUEST(0), 2: VinsRequest{1:id, 2:event_json, 4:is_silent, 18:TEXT_INPUT(1)}}.

use std::{
    io::Write,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

fn varint(mut v: u64, b: &mut Vec<u8>) {
    while v >= 0x80 {
        b.push(v as u8 | 0x80);
        v >>= 7;
    }
    b.push(v as u8);
}

fn pb_int(field: u64, v: u64, b: &mut Vec<u8>) {
    varint(field << 3, b);
    varint(v, b);
}

fn pb_bytes(field: u64, v: &[u8], b: &mut Vec<u8>) {
    varint(field << 3 | 2, b);
    varint(v.len() as u64, b);
    b.extend_from_slice(v);
}

fn frame(text: &str, silent: bool) -> Vec<u8> {
    let ev = serde_json::json!({"type": "text_input", "text": text}).to_string();
    let ns = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
    let mut vins = vec![];
    pb_bytes(1, format!("jarvis-{ns}").as_bytes(), &mut vins);
    pb_bytes(2, ev.as_bytes(), &mut vins);
    pb_int(4, silent as u64, &mut vins);
    pb_int(18, 1, &mut vins); // TEXT_INPUT
    let mut method = vec![];
    pb_int(1, 0, &mut method); // START_REQUEST
    pb_bytes(2, &vins, &mut method);
    let mut remote = vec![];
    pb_bytes(7, b"AliceCapability", &mut remote);
    pb_bytes(8, &method, &mut remote);
    let mut env = vec![];
    pb_bytes(252, &remote, &mut env);
    pb_int(253, 210, &mut env);
    let mut out = (env.len() as u32).to_le_bytes().to_vec();
    out.extend(env);
    out
}

/// Отправить Алисе текстовый запрос. silent=false — она ответит голосом, как обычно. Блокирует ~0,2 с.
pub fn text(text: &str, silent: bool) -> Result<(), String> {
    let addr = "127.0.0.1:9875".parse().unwrap();
    let mut c = std::net::TcpStream::connect_timeout(&addr, Duration::from_secs(1)).map_err(|e| format!("Алиса недоступна: {e}"))?;
    let _ = c.set_write_timeout(Some(Duration::from_secs(2)));
    let r = c.write_all(&frame(text, silent)).map_err(|e| e.to_string());
    std::thread::sleep(Duration::from_millis(150)); // дать aliced прочитать кадр до закрытия
    r
}

pub const MAIND_LOG: &str = "/data/quasar/daemons_logs/maind.log";

/// Читать новые строки журнала maind (переживает ротацию): каждая строка — в on_line. Свой поток.
pub fn follow_log(path: &'static str, on_line: impl Fn(&str) + Send + 'static) {
    std::thread::Builder::new()
        .name("maind-log".into())
        .spawn(move || {
            use std::io::{BufRead, BufReader, Seek, SeekFrom};
            let mut cur: Option<(u64, BufReader<std::fs::File>)> = None;
            let mut first = true;
            let mut part = String::new();
            loop {
                std::thread::sleep(Duration::from_millis(150));
                let Ok(st) = std::fs::metadata(path) else { continue };
                let ino = inode(&st);
                if cur.as_ref().is_none_or(|(i, _)| *i != ino) {
                    let Ok(mut f) = std::fs::File::open(path) else { continue };
                    if first {
                        let _ = f.seek(SeekFrom::End(0)); // старые активации не переигрываем
                        first = false;
                    }
                    part.clear();
                    cur = Some((ino, BufReader::with_capacity(8192, f)));
                }
                let (_, rd) = cur.as_mut().unwrap();
                loop {
                    match rd.read_line(&mut part) {
                        Ok(0) | Err(_) => break,
                        Ok(_) if part.ends_with('\n') => {
                            on_line(&part);
                            part.clear();
                        }
                        Ok(_) => {} // строка ещё дописывается — ждём продолжения
                    }
                }
            }
        })
        .expect("поток журнала maind");
}

#[cfg(unix)]
fn inode(m: &std::fs::Metadata) -> u64 {
    use std::os::unix::fs::MetadataExt;
    m.ino()
}

#[cfg(not(unix))]
fn inode(_: &std::fs::Metadata) -> u64 {
    1
}

const LAUNCH: &str = "/system/vendor/quasar/launch.json";
const RESTART_MARK: &str = "/tmp/jarvisd/alice-mic-restarted"; // /tmp в памяти: не больше раза за загрузку
const RUN_SCRIPT: &str = "/system/vendor/quasar/run.sh";
const RESTART_SHELL: &str = "killall quasar_launcher2; i=0; while pidof maind >/dev/null && [ $i -lt 20 ]; do sleep 0.5; i=$((i+1)); done; \
killall -9 maind 2>/dev/null; sleep 1; start-stop-daemon -S -b -x /system/vendor/quasar/run.sh";

/// Профиль с vqe_tap.so подложен, Алиса работает, но библиотеки в ней нет — она запустилась раньше нас.
fn misses_tap(mounts: &str, maps: &[String]) -> bool {
    mounts.contains(&format!(" {LAUNCH} ")) && !maps.is_empty() && !maps.iter().any(|m| m.contains("vqe_tap"))
}

fn platformd_maps() -> Vec<String> {
    let mut out = vec![];
    for d in std::fs::read_dir("/proc").into_iter().flatten().flatten() {
        let p = d.path();
        if !p.file_name().is_some_and(|n| n.to_string_lossy().bytes().all(|c| c.is_ascii_digit())) {
            continue;
        }
        let Ok(cmd) = std::fs::read(p.join("cmdline")) else { continue };
        let cmd = String::from_utf8_lossy(&cmd);
        if cmd.contains("maind") && cmd.contains("--platformd")
            && let Ok(m) = std::fs::read_to_string(p.join("maps"))
        {
            out.push(m);
        }
    }
    out
}

/// На Midi/Mini Алиса стартует раньше нас со штатным профилем — микрофона нет, пока её не перезапустить.
pub fn watch_mic(mic: std::sync::Arc<crate::audio::Mic>) {
    std::thread::spawn(move || {
        std::thread::sleep(Duration::from_secs(25));
        if mic.online() {
            return;
        }
        let mounts = std::fs::read_to_string("/proc/mounts").unwrap_or_default();
        if !misses_tap(&mounts, &platformd_maps()) {
            return;
        }
        if std::path::Path::new(RESTART_MARK).exists() {
            log::info!("микрофон: Алиса снова без vqe_tap.so — второй раз не перезапускаю, нужна перезагрузка колонки");
            return;
        }
        if !std::path::Path::new(RUN_SCRIPT).exists() {
            log::info!("микрофон: Алиса запустилась раньше Джарвиса, а {RUN_SCRIPT} нет — перезапускать не рискую, перезагрузите колонку");
            return;
        }
        let _ = std::fs::write(RESTART_MARK, "1");
        log::info!("микрофон: Алиса запустилась раньше Джарвиса (без vqe_tap.so) — перезапускаю Алису");
        match std::process::Command::new("/bin/sh").args(["-c", RESTART_SHELL]).output() {
            Ok(o) if o.status.success() => {}
            Ok(o) => return log::info!("микрофон: перезапуск Алисы не удался: {}", String::from_utf8_lossy(&o.stderr).trim()),
            Err(e) => return log::info!("микрофон: перезапуск Алисы не удался: {e}"),
        }
        for i in 1..=60 {
            std::thread::sleep(Duration::from_secs(1));
            if mic.online() {
                return log::info!("микрофон: после перезапуска Алисы на связи ({i} с)");
            }
        }
        log::info!("микрофон: после перезапуска Алисы звука всё ещё нет");
    });
}

#[cfg(test)]
mod tests {
    #[test]
    fn frame_layout() {
        let f = super::frame("пауза", true);
        let n = u32::from_le_bytes(f[..4].try_into().unwrap()) as usize;
        assert_eq!(n, f.len() - 4);
        assert_eq!(&f[4..6], &[0xe2, 0x0f]); // поле 252, длина следом
        assert!(f.ends_with(&[0xe8, 0x0f, 0xd2, 0x01])); // 253=210
        assert!(super::misses_tap(" /system/vendor/quasar/launch.json ", &["libc.so".into()]));
    }
}
