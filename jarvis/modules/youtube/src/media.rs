//! Bounded byte-range input and Opus decoding for YouTube's WebM audio.
use crate::remote::Stream;
use jarvis_home_common::bytes;
use libopus_sys as opus;
use reqwest::{Client, StatusCode};
use std::{
    io,
    process::Stdio,
    ptr::NonNull,
    time::{Duration, Instant},
};
use tokio::io::AsyncWriteExt;

const CHUNK: u64 = 256 << 10;
const CLUSTER: u32 = 0x1f43b675;

struct Input {
    client: Client,
    stream: Stream,
    pos: u64,
    total: u64,
    data: Vec<u8>,
    index: usize,
    chunk: u64,
}

impl Input {
    fn new(client: Client, stream: Stream, pos: u64) -> Self {
        let total = stream.length;
        Self {
            client,
            stream,
            pos,
            total,
            data: Vec::new(),
            index: 0,
            chunk: CHUNK,
        }
    }

    async fn refill(&mut self) -> io::Result<bool> {
        if self.index < self.data.len() {
            return Ok(true);
        }
        if self.total != 0 && self.pos >= self.total {
            return Ok(false);
        }
        let mut last = io::Error::other("YouTube не отдал звук");
        for retry in 0..4 {
            let end = if self.total == 0 {
                self.pos + self.chunk - 1
            } else {
                (self.pos + self.chunk - 1).min(self.total - 1)
            };
            let response = self
                .client
                .get(self.stream.url.clone())
                .header("User-Agent", self.stream.agent)
                .header("Range", format!("bytes={}-{}", self.pos, end))
                .send()
                .await;
            match response {
                Ok(response)
                    if response.status() == StatusCode::FORBIDDEN && self.chunk > 64 << 10 =>
                {
                    self.chunk /= 2;
                    continue;
                }
                Ok(response) if response.status() == StatusCode::PARTIAL_CONTENT => {
                    let (start, last_byte, total) = content_range(&response)
                        .filter(|&(start, last, total)| {
                            start == self.pos && (start..=end).contains(&last) && total > last
                        })
                        .ok_or_else(|| invalid("Неверный диапазон аудио YouTube"))?;
                    if self.total != 0 && self.total != total {
                        return Err(invalid("Размер аудио YouTube изменился"));
                    }
                    match bytes(response, self.chunk as usize).await {
                        Ok(data) if data.len() as u64 == last_byte - start + 1 => {
                            self.total = total;
                            self.data = data;
                            self.index = 0;
                            return Ok(true);
                        }
                        _ => last = io::Error::other("Соединение с аудио YouTube оборвалось"),
                    }
                }
                Ok(response) if response.status() == StatusCode::OK && self.pos == 0 => {
                    let data = bytes(response, self.chunk as usize)
                        .await
                        .map_err(io::Error::other)?;
                    if self.total != 0 && self.total != data.len() as u64 {
                        return Err(invalid("Неполный ответ YouTube"));
                    }
                    self.total = data.len() as u64;
                    self.data = data;
                    self.index = 0;
                    return Ok(!self.data.is_empty());
                }
                Ok(response) => {
                    let status = response.status();
                    last = io::Error::other(format!("YouTube не отдал звук ({status})"));
                    if !status.is_server_error() {
                        return Err(last);
                    }
                }
                Err(_) => last = io::Error::other("Соединение с аудио YouTube оборвалось"),
            }
            if retry < 3 {
                tokio::time::sleep(Duration::from_millis(250 * (retry + 1))).await;
            }
        }
        Err(last)
    }

    async fn byte(&mut self) -> io::Result<Option<u8>> {
        if !self.refill().await? {
            return Ok(None);
        }
        let byte = self.data[self.index];
        self.index += 1;
        self.pos += 1;
        Ok(Some(byte))
    }

    async fn take(&mut self, mut len: u64, mut out: Option<&mut [u8]>) -> io::Result<()> {
        while len > 0 {
            if !self.refill().await? {
                return Err(truncated());
            }
            let n = len.min((self.data.len() - self.index) as u64) as usize;
            let end = self.index + n;
            if let Some(buf) = out.take() {
                let (head, tail) = buf.split_at_mut(n);
                head.copy_from_slice(&self.data[self.index..end]);
                out = Some(tail);
            }
            self.index = end;
            self.pos += n as u64;
            len -= n as u64;
        }
        Ok(())
    }

    async fn vint(&mut self, first: u8, id: bool) -> io::Result<(u64, bool)> {
        let n = first.leading_zeros() + 1;
        if n > 8 || (id && n > 4) {
            return Err(invalid("Неверный элемент WebM"));
        }
        let mut value = u64::from(if id {
            first
        } else {
            first & (0xffu16 >> n) as u8
        });
        for _ in 1..n {
            value = (value << 8) | u64::from(self.byte().await?.ok_or_else(truncated)?);
        }
        Ok((value, !id && value == (1 << (7 * n)) - 1))
    }
}

fn content_range(response: &reqwest::Response) -> Option<(u64, u64, u64)> {
    let value = response.headers().get("Content-Range")?.to_str().ok()?;
    let (range, total) = value.strip_prefix("bytes ")?.split_once('/')?;
    let (start, end) = range.split_once('-')?;
    Some((start.parse().ok()?, end.parse().ok()?, total.parse().ok()?))
}

fn invalid(s: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, s)
}
fn truncated() -> io::Error {
    io::Error::new(io::ErrorKind::UnexpectedEof, "Обрыв дорожки WebM")
}

struct Webm {
    input: Input,
    cluster_ms: i64,
}

impl Webm {
    async fn sync(&mut self) -> io::Result<()> {
        let mut window = 0u32;
        for _ in 0..8 << 20 {
            window = (window << 8) | u32::from(self.input.byte().await?.ok_or_else(truncated)?);
            if window == CLUSTER {
                let first = self.input.byte().await?.ok_or_else(truncated)?;
                self.input.vint(first, false).await?;
                return Ok(());
            }
        }
        Err(invalid("В WebM нет начала кластера"))
    }

    async fn next(&mut self, packet: &mut [u8]) -> io::Result<Option<(usize, i64)>> {
        while let Some(first) = self.input.byte().await? {
            let (id, _) = self.input.vint(first, true).await?;
            let first = self.input.byte().await?.ok_or_else(truncated)?;
            let (size, unknown) = self.input.vint(first, false).await?;
            match id {
                0x18538067 | 0x1f43b675 | 0xa0 => {}
                0xe7 if !unknown && (1..=8).contains(&size) => {
                    let mut timestamp = [0; 8];
                    self.input
                        .take(size, Some(&mut timestamp[8 - size as usize..]))
                        .await?;
                    self.cluster_ms = i64::try_from(u64::from_be_bytes(timestamp))
                        .map_err(|_| invalid("Неверная метка времени WebM"))?;
                }
                0xa3 | 0xa1 if !unknown && size >= 4 && size <= packet.len() as u64 => {
                    self.input
                        .take(size, Some(&mut packet[..size as usize]))
                        .await?;
                    let offset = packet[0].leading_zeros() as usize + 1;
                    if offset > 8 || offset + 3 >= size as usize || packet[offset + 2] & 6 != 0 {
                        continue;
                    }
                    let relative = i16::from_be_bytes([packet[offset], packet[offset + 1]]);
                    let timestamp = self.cluster_ms.saturating_add(i64::from(relative));
                    packet.copy_within(offset + 3..size as usize, 0);
                    return Ok(Some((size as usize - offset - 3, timestamp)));
                }
                _ if !unknown => self.input.take(size, None).await?,
                _ => return Err(invalid("Элемент WebM неизвестной длины")),
            }
        }
        Ok(None)
    }
}

struct Decoder(NonNull<opus::OpusDecoder>);
// One playback task owns the decoder and uses it sequentially.
unsafe impl Send for Decoder {}
impl Decoder {
    fn new() -> io::Result<Self> {
        let mut error = 0;
        let pointer = unsafe { opus::opus_decoder_create(48000, 1, &mut error) };
        NonNull::new(pointer)
            .map(Self)
            .ok_or_else(|| io::Error::other(format!("Opus: {error}")))
    }

    fn decode(&mut self, packet: &[u8], pcm: &mut [i16]) -> io::Result<usize> {
        let count = unsafe {
            opus::opus_decode(
                self.0.as_ptr(),
                packet.as_ptr(),
                packet.len() as i32,
                pcm.as_mut_ptr(),
                pcm.len() as i32,
                0,
            )
        };
        usize::try_from(count).map_err(|_| invalid("Неверный кадр Opus"))
    }
}
impl Drop for Decoder {
    fn drop(&mut self) {
        unsafe { opus::opus_decoder_destroy(self.0.as_ptr()) };
    }
}

struct Aplay(Option<tokio::process::Child>);

impl Drop for Aplay {
    fn drop(&mut self) {
        let Some(mut child) = self.0.take() else {
            return;
        };
        if child.id().is_none() {
            return;
        }
        // aplay closes ALSA's shared mixer on SIGINT.
        #[cfg(unix)]
        if let Some(pid) = child.id() {
            unsafe { libc::kill(pid as libc::pid_t, libc::SIGINT) };
        }
        if let Ok(runtime) = tokio::runtime::Handle::try_current() {
            runtime.spawn(async move {
                if tokio::time::timeout(Duration::from_secs(1), child.wait())
                    .await
                    .is_err()
                {
                    let _ = child.kill().await;
                }
            });
        }
    }
}

async fn write_audio(stdin: &mut tokio::process::ChildStdin, data: &[u8]) -> io::Result<()> {
    tokio::time::timeout(Duration::from_secs(3), stdin.write_all(data))
        .await
        .map_err(|_| io::Error::other("ALSA не принимает звук"))?
}

pub async fn play(
    client: Client,
    stream: Stream,
    start: f64,
    mut progress: impl FnMut(f64),
    volume: impl Fn() -> f32,
) -> io::Result<()> {
    let mut audio = Audio::open(client, stream, start, volume()).await?;
    let (_, data) = audio
        .next(volume())
        .await?
        .ok_or_else(|| invalid("YouTube не вернул кадры Opus"))?;
    let child = tokio::process::Command::new("aplay")
        .args([
            "-q", "-D", "default", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "1",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .kill_on_drop(true)
        .spawn()?;
    let mut player = Aplay(Some(child));
    let child = player.0.as_mut().unwrap();
    let mut stdin = child
        .stdin
        .take()
        .ok_or_else(|| io::Error::other("Нет входа aplay"))?;
    let lead = Duration::from_millis(120);
    let mut next_write = Instant::now();
    write_audio(&mut stdin, data).await?;
    next_write += Duration::from_secs_f64(data.len() as f64 / 96_000.0);
    progress(start);
    while let Some((timestamp, data)) = audio.next(volume()).await? {
        // ALSA may accept a long buffer immediately. Keep the producer close to real time
        // so pause and saved positions refer to audio the speaker has had time to play.
        if let Some(deadline) = next_write.checked_sub(lead) {
            tokio::time::sleep_until(deadline.into()).await;
        }
        write_audio(&mut stdin, data).await?;
        next_write =
            next_write.max(Instant::now()) + Duration::from_secs_f64(data.len() as f64 / 96_000.0);
        progress((timestamp - lead.as_secs_f64()).max(start));
    }
    drop(stdin);
    let status = tokio::time::timeout(Duration::from_secs(3), child.wait())
        .await
        .map_err(|_| io::Error::other("ALSA не завершила воспроизведение"))??;
    if status.success() {
        Ok(())
    } else {
        Err(io::Error::other("aplay остановился"))
    }
}

pub struct Audio {
    webm: Webm,
    decoder: Decoder,
    first: Option<(usize, i64)>,
    start: f64,
    gain: f32,
    packet: [u8; 8192],
    pcm: [i16; 5760],
    out: [u8; 11520],
}
impl Audio {
    pub async fn open(client: Client, stream: Stream, start: f64, gain: f32) -> io::Result<Self> {
        tokio::time::sleep_until(stream.ready.into()).await;
        let offset = if start > 0.5 && stream.duration > 0.0 {
            ((start / stream.duration * stream.length as f64) as u64)
                .saturating_sub(128 << 10)
                .min(stream.length.saturating_sub(64 << 10))
        } else {
            0
        };
        let mut webm = Webm {
            input: Input::new(client.clone(), stream.clone(), offset),
            cluster_ms: 0,
        };
        let mut packet = [0; 8192];
        let mut first = None;
        if offset > 0 {
            if webm.sync().await.is_ok()
                && let Ok(Some(frame)) = webm.next(&mut packet).await
                && frame.1 <= (start * 1000.0) as i64
            {
                first = Some(frame);
            } else {
                webm = Webm {
                    input: Input::new(client, stream, 0),
                    cluster_ms: 0,
                };
            }
        }
        Ok(Self {
            webm,
            decoder: Decoder::new()?,
            first,
            start,
            gain,
            packet,
            pcm: [0; 5760],
            out: [0; 11520],
        })
    }

    pub async fn next(&mut self, target: f32) -> io::Result<Option<(f64, &[u8])>> {
        loop {
            let frame = match self.first.take() {
                Some(frame) => Some(frame),
                None => self.webm.next(&mut self.packet).await?,
            };
            let Some((len, timestamp)) = frame else {
                return Ok(None);
            };
            let sec = (timestamp as f64 / 1000.0).max(0.0);
            if sec + 0.12 < self.start {
                continue;
            }
            let count = self.decoder.decode(&self.packet[..len], &mut self.pcm)?;
            if sec < self.start {
                continue;
            }
            self.gain = if target < self.gain {
                target
            } else {
                self.gain + (target - self.gain) * 0.1
            };
            for (sample, bytes) in self.pcm[..count].iter().zip(self.out.chunks_exact_mut(2)) {
                bytes.copy_from_slice(&((*sample as f32 * self.gain) as i16).to_le_bytes());
            }
            return Ok(Some((sec, &self.out[..count * 2])));
        }
    }
}
