//! Мгновенные команды во время разговора: «стоп», «хватит», «пауза», «громче», «тише». Их слышит
//! штатный распознаватель команд Станции (программа spotcmd рядом с ядром) прямо на колонке,
//! без интернета и без расшифровки. Запущен он только пока Джарвис занят: в остальное время
//! процессор не тратится.

use std::{
    io::{BufRead, BufReader, Write},
    path::{Path, PathBuf},
    process::{Child, ChildStdin, Command, Stdio},
    sync::{
        Arc, Mutex,
        mpsc::{SyncSender, TrySendError, sync_channel},
    },
    time::{Duration, Instant},
};

pub type OnHit = Arc<dyn Fn(String) + Send + Sync>;

#[derive(Default)]
struct St {
    child: Option<u32>,
    feed: Option<SyncSender<Vec<i16>>>,
    last_on: Option<Instant>,
    broken: Option<Instant>, // последний неудачный запуск: не перезапускать чаще раза в минуту
}

pub struct CmdSpotter {
    path: PathBuf,
    on_hit: OnHit,
    st: Arc<Mutex<St>>,
}

impl CmdSpotter {
    /// None — программы spotcmd рядом нет (не та колонка или старая установка).
    pub fn new(data: &Path, on_hit: OnHit) -> Option<CmdSpotter> {
        let path = data.join("spotcmd");
        path.exists().then(|| CmdSpotter { path, on_hit, st: Arc::default() })
    }

    /// Кадр микрофона, пока Джарвис занят. Запускает распознаватель, если он ещё не работает.
    pub fn feed(&self, pcm: &[i16]) {
        let mut st = self.st.lock().unwrap();
        st.last_on = Some(Instant::now());
        if st.child.is_none() && st.broken.is_none_or(|t| t.elapsed() > Duration::from_secs(60)) {
            self.start(&mut st);
        }
        if let Some(f) = &st.feed
            && let Err(TrySendError::Disconnected(_)) = f.try_send(pcm.to_vec())
        {
            st.feed = None; // не успевает — кадр пропускаем, Джарвис не ждёт
        }
    }

    /// Джарвис свободен: через 15 с распознаватель выключается (закрытый вход — он завершится сам).
    pub fn idle(&self) {
        let mut st = self.st.lock().unwrap();
        if st.child.is_some() && st.last_on.is_some_and(|t| t.elapsed() > Duration::from_secs(15)) {
            st.child = None;
            st.feed = None;
        }
    }

    fn start(&self, st: &mut St) {
        let mut child: Child = match Command::new(&self.path).stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::null()).spawn() {
            Ok(c) => c,
            Err(e) => {
                log::info!("команды: распознаватель не запустился: {e}");
                st.broken = Some(Instant::now());
                return;
            }
        };
        let (Some(stdin), Some(stdout)) = (child.stdin.take(), child.stdout.take()) else {
            let _ = child.kill();
            st.broken = Some(Instant::now());
            return;
        };
        let (tx, rx) = sync_channel::<Vec<i16>>(64);
        let pid = child.id();
        st.child = Some(pid);
        st.feed = Some(tx);
        let started = Instant::now();
        std::thread::spawn(move || {
            let mut stdin: ChildStdin = stdin;
            for pcm in rx {
                let buf: Vec<u8> = pcm.iter().flat_map(|v| v.to_le_bytes()).collect();
                if stdin.write_all(&buf).is_err() {
                    break;
                }
            }
        });
        let (on_hit, shared) = (self.on_hit.clone(), self.st.clone());
        std::thread::spawn(move || {
            for line in BufReader::new(stdout).lines().map_while(Result::ok) {
                if let Some(p) = line.strip_prefix("HIT ") {
                    on_hit(p.trim().to_string());
                }
            }
            let status = child.wait();
            let mut st = shared.lock().unwrap();
            if st.child == Some(pid) {
                st.child = None;
                st.feed = None;
                if started.elapsed() < Duration::from_secs(5) {
                    log::info!("команды: распознаватель сразу завершился ({status:?}) — попробую через минуту");
                    st.broken = Some(Instant::now());
                }
            }
        });
    }
}
