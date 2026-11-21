// Плеер: карточка на главной и отдельная страница.
import { $, $$, Motion, act, api, copyText, icon, jelly, toast } from "./core.js";
import { S } from "./main.js";

// ---------------------------------------------------------------- плеер на главной (Glass player, Kokonut UI «Now Playing», MIT)
// Пластинка с обложкой видео: разгоняется и останавливается по инерции (τ 0,22 / 0,2 с), 33⅓ об/мин — 200°/с;
// её можно крутить пальцем — это перемотка. Тонарм опускается за 500 мс, эквалайзер дышит в такт,
// цифры времени перекатываются (220 мс). Карточка всегда одной высоты.
export let M = null, mAt = 0;
const musicListeners = new Set();
let musicReadPending = false;
let musicWrites = 0;
let musicRevision = 0;
let bufferingTimer = 0;
export const NP = { angle: 0, omega: 0, env: 0, held: false, scrub: null, lastA: 0, lastT: 0, samples: [], seekHeld: null };
export const CRUISE = 200, SPIN_UP = 0.22, SPIN_DOWN = 0.2, COAST = 0.45;
export const clockStr = (s) => { s = Math.max(0, Math.floor(s || 0)); return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`; };
export const mPlaying = () => !!M?.playing && !M.paused;
export const mPos = () => !M ? 0 : NP.scrub != null ? NP.scrub : NP.seekHeld != null ? NP.seekHeld : mPlaying() && !M.loading ? Math.min(M.dur || Infinity, M.pos + (Date.now() - mAt) / 1000) : M.pos;
export const hash = (n) => { const s = Math.sin(n * 127.1) * 43758.5453; return s - Math.floor(s); };
export const noise = (x) => { const i = Math.floor(x), f = x - i, u = f * f * (3 - 2 * f); return hash(i) * (1 - u) + hash(i + 1) * u; };

export const ARM = `<svg class="np-arm" viewBox="0 0 40 110" aria-hidden="true"><circle class="a1" cx="31" cy="11" r="8"/><circle class="a2" cx="31" cy="11" r="3"/><path class="a3" d="M31 11 L31 70 L18 92"/><rect class="a4" height="12" rx="1.5" transform="rotate(32 16 96)" width="7" x="12.5" y="90"/></svg>`;

export function playerHtml() {
  return `<section class="card np" id="np" data-state="idle">
    <div class="mu-top">
      <a class="cover" href="#player" id="np-cover" aria-label="Открыть плеер">${icon("music")}<img id="np-art" alt=""></a>
      <div class="np-info"><div class="np-src" id="np-kicker"></div><div class="np-title" id="np-title">Тишина</div><div class="np-artist" id="np-artist">Включите что-нибудь с YouTube или попросите Алису</div></div>
    </div>
    <div class="progress np-play-ui"><span class="roll" id="np-cur"></span><div id="np-seek"></div><span id="np-dur"></span></div>
    <div class="controls np-play-ui">
      <button class="cbtn" type="button" data-m="stop" aria-label="Выключить">${icon("stop", "solid")}</button>
      <button class="cbtn" type="button" data-m="prev" id="np-prev" aria-label="Предыдущий">${icon("prev", "solid")}</button>
      <button class="cbtn big" type="button" data-m="toggle" id="np-toggle" aria-label="Пауза">${icon("play", "solid")}</button>
      <button class="cbtn" type="button" data-m="next" id="np-next" aria-label="Следующий">${icon("skip", "solid")}</button>
      <button class="cbtn" type="button" data-m="copy" aria-label="Скопировать ссылку">${icon("link")}</button>
    </div>
    <form class="np-form" id="np-form"><input type="text" id="np-q" placeholder="Название или ссылка на YouTube" aria-label="Что включить" autocomplete="off"><button class="cbtn big" type="submit" aria-label="Включить">${icon("play", "solid")}</button></form>
  </section>
  ${volCardHtml()}`;
}

// громкость колонки — отдельная карточка (на главной и на странице «Плеер»)
export function volCardHtml() {
  return `<section class="card vol-card" id="np-vol">
    <div class="card-head"><h2>Громкость колонки</h2><span class="meta">Алиса, музыка и Джарвис</span></div>
    <div class="vol-main">
      <div class="vol-num"><b class="vol-big" id="vol-big">–</b><small>из 10</small></div>
      <div class="vol-ctl">
        <button class="cbtn" type="button" data-vol="-1" aria-label="Тише">${icon("minus")}</button>
        <div class="vol-sl"><div id="vol"></div><div class="vol-ticks" aria-hidden="true">${Array.from({ length: 11 }, (_, i) => `<i data-n="${i}"></i>`).join("")}</div></div>
        <button class="cbtn" type="button" data-vol="1" aria-label="Громче">${icon("plus")}</button>
      </div>
    </div>
  </section>`;
}

function renderMusic() {
  drawPlayer();
  $("#nav-eq")?.classList.toggle("hidden", !mPlaying());
  for (const listener of musicListeners) listener(M);
}

function acceptMusic(state) {
  M = state;
  mAt = Date.now();
  clearTimeout(bufferingTimer);
  bufferingTimer = 0;
  if (state?.playing && state?.loading) {
    bufferingTimer = setTimeout(() => { bufferingTimer = 0; void musicData(); }, 400);
  }
  renderMusic();
}

export function subscribeMusic(listener) {
  listener(M);
  musicListeners.add(listener);
  return () => musicListeners.delete(listener);
}

export async function musicData() {
  if (musicReadPending || musicWrites) return;
  musicReadPending = true;
  const revision = musicRevision;
  try {
    const state = await api("GET", "/api/music");
    if (revision === musicRevision) acceptMusic(state);
  } catch {
    // Следующий опрос повторит запрос.
  } finally {
    musicReadPending = false;
  }
}

// обложка: YouTube — крупная (если есть), иначе средняя; Яндекс Музыка — как пришла
export function setArt(img, art, fallback) {
  if (img.dataset.src === art) return;
  img.dataset.src = art; img.classList.remove("ok");
  if (!art) { img.removeAttribute("src"); return; }
  const canFallback = () => fallback && fallback !== art && img.getAttribute("src") === art;
  img.onload = () => {
    if (img.naturalWidth <= 120 && canFallback()) { img.src = fallback; return; }
    img.classList.add("ok");
  };
  img.onerror = () => { if (canFallback()) img.src = fallback; };
  img.src = art;
}

export function drawPlayer() {
  const np = $("#np");
  if (!np || !M) return;
  const t = M.track || {}, on = M.playing || M.paused, ya = M.source === "yandex";
  np.dataset.state = M.loading && !on ? "loading" : !on ? "idle" : M.paused ? "paused" : "playing";
  setArt($("#np-art"), on ? artOf(t, true) : "", artOf(t, false));
  $("#np-kicker").innerHTML = on ? `<span class="np-eq" aria-hidden="true"><i></i><i></i><i></i></span>${ya ? "Яндекс Музыка" : "YouTube"}${M.paused ? " · пауза" : ""}` : M.loading ? "ищу…" : "Плеер";
  swapText($("#np-title"), on ? t.Title || "…" : M.loading ? "Ищу и включаю…" : "Тишина");
  swapText($("#np-artist"), on ? t.Author || (ya ? "Яндекс Музыка" : "YouTube") : "Включите что-нибудь с YouTube или попросите Алису");
  $("#np-toggle").innerHTML = icon(M.paused ? "play" : "pause", "solid");
  $("#np-toggle").setAttribute("aria-label", M.paused ? "Продолжить" : "Пауза");
  $("#np-prev").disabled = !ya && (M.history || []).length < 2;
  $("#np-next").disabled = !ya && !(M.next || []).length;
  $("#np-dur").textContent = M.dur ? clockStr(M.dur) : "";
  Motion.add(npAnim);
}

// управление музыкой — общее для главной и страницы «Плеер»
export function musicAct(a, b, { disableControl = true } = {}) {
  if (!M) return;
  if (b) { b.classList.remove("k-prev", "k-next", "k-pop"); void b.offsetWidth; b.classList.add(a === "prev" ? "k-prev" : a === "next" ? "k-next" : "k-pop"); }
  const control = disableControl ? b : undefined;
  if (a === "copy") {
    const link = musicLink();
    if (link) return copyText(link).then(() => toast("Ссылка скопирована"), () => toast("Не получилось скопировать", true));
    return;
  }
  if (a === "radio") {
    return music({ action: "radio", on: !M.radio }, control)
      .then((ok) => ok && toast(M.radio ? "Радио: после очереди — похожее" : "Радио выключено: после очереди тишина"));
  }
  if (a === "repeat") {
    return music({ action: "repeat", on: !M.repeat }, control)
      .then((ok) => ok && toast(M.repeat ? "Повтор трека включён" : "Повтор выключен"));
  }
  if (a === "prev" && M.source !== "yandex") {
    const previous = M.history?.[1];
    if (previous) return music({ action: "id", query: previous.ID }, control);
    return;
  }
  return music({ action: a === "toggle" ? (M.paused ? "resume" : "pause") : a }, control);
}

// перекатывающиеся цифры: сменившаяся уезжает вверх, новая въезжает снизу (назад — наоборот)
export function rollTo(el, text, dir) {
  if (el.dataset.v === text) return;
  const old = el.dataset.v || "";
  el.dataset.v = text;
  if (Motion.reduce || old.length !== text.length) { el.innerHTML = [...text].map((ch) => `<span class="rd"><span>${ch}</span></span>`).join(""); return; }
  [...text].forEach((ch, i) => {
    const slot = el.children[i];
    if (!slot || old[i] === ch) return;
    slot.innerHTML = `<span class="out ${dir > 0 ? "up" : "down"}">${old[i]}</span><span class="in ${dir > 0 ? "up" : "down"}">${ch}</span>`;
  });
}

export const npAnim = {
  lastSec: -1,
  t: 0,
  frame(dt) { this.t += dt; },
  render() {
    const p = mPos(), t = Motion.reduce ? 0.37 : this.t, env = mPlaying() ? 1 : 0;
    $$(".np-eq i").forEach((bar, i) => {
      const lvl = 0.18 + 0.82 * (0.6 * noise(t * 4.3 + i * 17.3) + 0.4 * noise(t * 9.1 + i * 31.7 + 100)) * env;
      bar.style.transform = `scaleY(${lvl.toFixed(3)})`;
    });
    if (seekJ && M && M.dur && !seekJ.dragging) { seekJ.range(0, M.dur); seekJ.set(p); }
    const sec = Math.floor(p), cur = $("#np-cur") || $("#pl-cur");
    if (cur && (sec !== this.lastSec || cur !== this.lastEl)) { rollTo(cur, M && (M.playing || M.paused) ? clockStr(p) : "", sec >= this.lastSec ? 1 : -1); this.lastSec = sec; this.lastEl = cur; }
  },
  done() { return (!$("#np") && !$("#plh")) || !mPlaying(); },
};

export function playerSetup() {
  const np = $("#np");
  np.addEventListener("click", (e) => {
    const b = e.target.closest("[data-m]");
    if (!b || b.disabled) return;
    musicAct(b.dataset.m, b);
  });
  $("#np-form").addEventListener("submit", (e) => {
    e.preventDefault();
    const q = $("#np-q").value.trim();
    if (!q) return;
    $("#np-q").value = "";
    void playMusicQuery(q);
  });
  makeSliders($("#np-seek"), $("#vol"));
}

// перемотка и громкость — «желейные» ползунки (на главной и в плеере)
export function makeSliders(seekEl, volEl) {
  seekJ?.destroy();
  volJ?.destroy();
  NP.seekHeld = null;
  if (volWant == null) volDrag = false;
  const seek = jelly(seekEl, { label: "Перемотка", min: 0, max: Math.max(1, M?.dur || 1), value: 0, format: clockStr, keyStep: 5,
    disabled: () => !M?.dur,
    onInput: (v) => { NP.seekHeld = v; Motion.add(npAnim); },
    onChange: (v) => { NP.seekHeld = null; if (!M?.dur) return; M.pos = v; mAt = Date.now(); Motion.add(npAnim); music({ action: "seek", sec: Math.round(v) }); } });
  const volume = jelly(volEl, { label: "Громкость колонки", min: 0, max: 10, step: 1, value: S.speaker.volume >= 0 ? S.speaker.volume : 5, format: (v) => String(v),
    onInput: (v) => { volDrag = true; volShow(v); },
    onChange: (v) => setVolume(v) });
  seekJ = seek;
  volJ = volume;
  const card = volEl.closest(".vol-card");
  card?.addEventListener("click", (e) => {
    const b = e.target.closest("[data-vol]");
    if (!b) return;
    const cur = volWant ?? (S.speaker.volume >= 0 ? S.speaker.volume : 5);
    const v = Motion.clamp(cur + Number(b.dataset.vol), 0, 10);
    b.classList.remove("k-pop"); void b.offsetWidth; b.classList.add("k-pop");
    if (v === cur) return;
    volJ.set(v, true); volShow(v); setVolume(v);
  });
  if (S.speaker.volume >= 0) volShow(S.speaker.volume);
  return () => {
    seek.destroy();
    volume.destroy();
    if (seekJ === seek) {
      seekJ = null;
      NP.seekHeld = null;
    }
    if (volJ === volume) {
      volJ = null;
      if (volWant == null) volDrag = false;
    }
  };
}

// число и деления под ползунком; отправка с задержкой — чтобы частые «+» не долбили колонку
export let volWant = null, volTimer = 0;
export function volShow(v) {
  const big = $("#vol-big"); if (big) { big.textContent = v; big.dataset.v = v; }
  $$(".vol-ticks i").forEach((t) => t.classList.toggle("on", Number(t.dataset.n) <= v));
  const c = $("#np-vol"); if (c) c.dataset.lvl = v === 0 ? "mute" : v <= 3 ? "low" : v <= 7 ? "mid" : "high";
}
export function setVolume(v) {
  volDrag = true; volWant = v; volShow(v);
  clearTimeout(volTimer);
  volTimer = setTimeout(() => {
    api("POST", "/api/speaker/volume", { level: v }).then((r) => { S.speaker.volume = r.volume; })
      .catch((e) => toast(e.message, true)).finally(() => { if (volWant === v) { volWant = null; volDrag = false; } });
  }, 250);
}

export async function music(body, btn) {
  const revision = ++musicRevision;
  musicWrites++;
  try {
    let state;
    await act(btn, async () => { state = await api("POST", "/api/music", body); });
    if (!state) return false;
    if (revision === musicRevision) acceptMusic(state);
    return true;
  } finally {
    musicWrites--;
  }
}

export async function playMusicQuery(query) {
  const before = M;
  const loading = { ...before, loading: true };
  M = loading;
  renderMusic();
  const ok = await music({ action: "play", query });
  if (!ok && M === loading) {
    M = before || { loading: false };
    renderMusic();
  }
  return ok;
}

// громкость колонки (её слышно у Алисы, музыки и Джарвиса); неизвестна — ползунок прячем
export let volDrag = false, seekJ = null, volJ = null;
export function drawVolume() {
  const box = $("#np-vol");
  if (!box || !volJ || volDrag) return;
  const v = S.speaker.volume;
  box.classList.toggle("hidden", v == null || v < 0);
  if (v != null && v >= 0) { volJ.set(v, true); volShow(v); }
}

export function artOf(track, big, state = M) {
  if (!track?.ID) return "";
  if (state?.source === "yandex") return state.cover || "";
  return `https://i.ytimg.com/vi/${encodeURIComponent(track.ID)}/${big ? "maxresdefault" : "mqdefault"}.jpg`;
}

export function musicLink(state = M) {
  return state?.track?.ID ? state.link || `https://youtu.be/${encodeURIComponent(state.track.ID)}` : "";
}

function swapText(el, text) {
  if (el.title === text && el.firstChild) return;
  el.title = text;
  el.textContent = text;
  el.classList.remove("swap");
  void el.offsetWidth;
  el.classList.add("swap");
}
