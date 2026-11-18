// Обучение слова «Джарвис» и запись фона.
import { $, Motion, act, api, esc, icon, sec } from "./core.js";
import { page } from "./main.js";

const TRAIN = "/api/p/training/train";

// ---------------------------------------------------------------- обучение слова
export const KIND = { pos: "Джарвис", false: "Не Джарвис", near: "Не проверено", bg: "Фон" };
export const GUESS = { pos: "колонка решила, что звали", false: "колонка сработала, но решила, что зря", near: "колонка насторожилась, но не сработала" };
export let T = null, skipped = new Set(), clipFilter = "all", clipLimit = 40;

export const clipUrl = (x) => `${TRAIN}/clip?kind=${x.kind}&name=${encodeURIComponent(x.name)}`;
export const clipWhen = (x) => new Date(x.time).toLocaleString("ru-RU", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
export const clipProb = (x) => (x.name.match(/_p(\d+)/) || [])[1];
export const clipSec = (x) => Math.max(0, (x.size - 44) / 32000);
export const queue = () => (T?.clips || []).filter((x) => !x.reviewed && x.kind !== "bg" && !skipped.has(x.kind + "/" + x.name))
  .sort((a, b) => ({ near: 0, pos: 1, false: 2 }[a.kind] - { near: 0, pos: 1, false: 2 }[b.kind]) || (new Date(b.time) - new Date(a.time)));

export async function drawTrainingRecords(panel = $("#training-records")) {
  if (!panel?.isConnected) return;
  let data;
  try { data = await api("GET", TRAIN); } catch (e) {
    if (panel.isConnected) panel.innerHTML = `<p class="err">${esc(e.message)}</p>`;
    return;
  }
  if (!panel.isConnected) return;
  T = data;
  T.clips ||= [];
  panel.innerHTML = `${sec("Проверка записей «Джарвис»", "Прослушайте сохранённые записи и отметьте, где был оклик. Клавиши 1 и 2 отмечают ответ, пробел ставит запись на паузу.", `<div id="rv"></div>`)}
  ${sec("Дообучить на компьютере", "В архив входят размеченные записи, модель и скрипт дообучения.", trainStepsHtml())}
  ${sec("Все записи", "", `<div id="clips"></div>`)}`;
  drawReview(); drawClips();
  $("#t-restore")?.addEventListener("click", (e) => act(e.target, () => api("POST", "/api/train/model/restore"), "Прежняя модель сохранена для «Джарвиса»"));
}

export function trainStepsHtml() {
  const c = T.counts || {};
  return `<div class="row"><div class="chips"><span class="chip k-pos">Джарвис <b id="c-pos">${c.pos || 0}</b></span><span class="chip k-false">Не Джарвис <b id="c-false">${c.false || 0}</b></span><span class="chip">Фон <b>${c.bg || 0}</b></span></div>
      <span class="spacer"></span>${T.clips.length ? `<a class="btn primary" href="${TRAIN}/download" download>Скачать записи</a>` : `<button class="btn" disabled>Скачать записи</button>`}</div>
    <ol class="steps">
      <li><b>Скачайте и распакуйте архив.</b> Записи уже разложены по вашим ответам.</li>
      <li><b>Дообучите модель.</b> В распакованной папке установите зависимости командой <code>pip install torch numpy</code> и запустите <code>python finetune.py</code>.</li>
      <li><b>Загрузите</b> <code>wake_new.bin</code> в разделе «Обучить фразу» с именем «Джарвис». Проверьте и выберите модель в списке.</li></ol>
    ${T.prev_model ? `<div><button class="btn ghost" id="t-restore">Вернуть прежнюю модель «Джарвис»</button></div>` : ""}`;
}

// ---- проверка: плеер, который всё время играет текущую запись по кругу
export const RV = { audio: null, started: false, peaks: [], key: "" };
export function rvAudio() {
  if (!RV.audio) {
    RV.audio = new Audio(); RV.audio.loop = true; RV.audio.preload = "auto";
    RV.audio.addEventListener("play", () => { RV.started = true; $("#rv-toggle") && ($("#rv-toggle").innerHTML = icon("pause", "solid")); Motion.add(rvAnim); });
    RV.audio.addEventListener("pause", () => { $("#rv-toggle") && ($("#rv-toggle").innerHTML = icon("play", "solid")); });
  }
  return RV.audio;
}
export const rvAnim = {
  render() {
    const a = RV.audio, wave = $("#rv-wave");
    if (!a || !wave) return;
    const d = a.duration || 0, p = d ? a.currentTime / d : 0;
    const bars = wave.children, lit = Math.round(p * bars.length);
    for (let i = 0; i < bars.length; i++) bars[i].classList.toggle("on", i < lit);
    $("#rv-time").textContent = `${(a.currentTime || 0).toFixed(1)} / ${d ? d.toFixed(1) : "…"} с`;
  },
  frame() {},
  done() { return !RV.audio || RV.audio.paused || !$("#rv-wave"); },
};

export async function rvWave(url) {
  const wave = $("#rv-wave");
  try {
    const buf = await (await fetch(url, { credentials: "same-origin" })).arrayBuffer();
    const ctx = rvWave.ctx ||= new (window.AudioContext || window.webkitAudioContext)();
    const data = (await ctx.decodeAudioData(buf)).getChannelData(0);
    const n = 72, step = Math.floor(data.length / n), peaks = [];
    for (let i = 0; i < n; i++) { let m = 0; for (let j = i * step; j < (i + 1) * step; j++) m = Math.max(m, Math.abs(data[j] || 0)); peaks.push(m); }
    const top = Math.max(0.05, ...peaks);
    if (!wave || $("#rv-wave") !== wave) return;
    [...wave.children].forEach((b, i) => b.style.setProperty("--h", `${Math.max(6, (peaks[i] / top) * 100).toFixed(0)}%`));
  } catch { /* волна — украшение */ }
}

export function drawReview() {
  const q = queue(), x = q[0], box = $("#rv");
  if (!box) return;
  if (!x) {
    RV.audio?.pause();
    box.innerHTML = `<div class="rv-done">${icon("done")}<b>Всё проверено</b></div>`;
    return;
  }
  const p = clipProb(x), key = x.kind + "/" + x.name;
  box.innerHTML = `<div class="rvp">
    <div class="rvp-head"><span class="chip k-${x.kind}">${esc(GUESS[x.kind])}${p ? ` · ${p}%` : ""}</span><span class="spacer"></span><span class="muted">${esc(clipWhen(x))} · осталось ${q.length}</span></div>
    <div class="rvp-player">
      <button class="np-play" id="rv-toggle" type="button" aria-label="Пауза">${icon(RV.started ? "pause" : "play", "solid")}</button>
      <div class="rvp-wave" id="rv-wave" title="Нажмите — сыграть с этого места">${"<i></i>".repeat(72)}</div>
      <span class="rvp-time" id="rv-time">0.0 / … с</span>
    </div>
    <div class="rvp-q"><b>Было слово «Джарвис»?</b></div>
    <div class="rv-ans"><button class="btn primary" data-a="yes">Да, было <kbd>1</kbd></button><button class="btn" data-a="no">Нет <kbd>2</kbd></button></div>
    <div class="rv-alt"><button class="btn sm ghost" data-a="skip">Пропустить</button><button class="btn sm ghost danger" data-a="del">Не уверен — удалить</button></div>
  </div>`;
  const a = rvAudio();
  if (RV.key !== key) { RV.key = key; a.src = clipUrl(x); }
  // следующую запись (если она на Google Диске) колонка докачивает заранее — ответ «да/нет» без паузы
  const nx = q[1];
  if (nx?.remote && RV.pre !== nx.name) { RV.pre = nx.name; fetch(clipUrl(nx), { credentials: "same-origin" }).catch(() => {}); }
  rvWave(clipUrl(x));
  if (RV.started) a.play().catch(() => {});
  rvAnim.render();
  box.onclick = (e) => {
    const b = e.target.closest("[data-a]");
    if (b) return answer(b.dataset.a, b);
    if (e.target.closest("#rv-toggle")) return a.paused ? a.play().catch(() => {}) : a.pause();
    const w = e.target.closest("#rv-wave");
    if (w && a.duration) { const r = w.getBoundingClientRect(); a.currentTime = ((e.clientX - r.left) / r.width) * a.duration; a.play().catch(() => {}); }
  };
}

export async function answer(a, btn) {
  const x = queue()[0];
  if (!x) return;
  RV.started = true;   // ответили — дальше записи сами играют по кругу
  if (a === "skip") { skipped.add(x.kind + "/" + x.name); drawReview(); return; }
  await act(btn, async () => {
    await api("POST", TRAIN + "/move", { kind: x.kind, name: x.name, to: a === "yes" ? "pos" : a === "no" ? "false" : "delete", reviewed: true });
    T = await api("GET", TRAIN); T.clips ||= [];
  });
  const c = T.counts || {};
  if ($("#c-pos")) { $("#c-pos").textContent = c.pos || 0; $("#c-false").textContent = c.false || 0; }
  drawReview(); drawClips();
}

document.addEventListener("keydown", (e) => {
  if (page !== "training" || !$("#rv-toggle") || e.target.closest("input, textarea, select")) return;
  if (e.key === "1") answer("yes");
  else if (e.key === "2") answer("no");
  else if (e.key === " ") { e.preventDefault(); const a = rvAudio(); a.paused ? a.play().catch(() => {}) : a.pause(); }
});

// ---- все записи: аккуратный список, свой маленький плеер на строку
export const LIST = { audio: null, key: "" };
export function drawClips() {
  const box = $("#clips");
  if (!box) return;
  const c = T.counts || {}, all = T.clips;
  const shown = all.filter((x) => clipFilter === "all" || (clipFilter === "todo" ? !x.reviewed && x.kind !== "bg" : x.kind === clipFilter));
  const tabs = [["all", "Все", all.length], ["pos", "Джарвис", c.pos || 0], ["false", "Не Джарвис", c.false || 0], ["bg", "Фон", c.bg || 0], ["todo", "Не проверено", all.filter((x) => !x.reviewed && x.kind !== "bg").length]];
  box.innerHTML = `<div class="row"><div class="seg" id="cl-f">${tabs.map(([k, n, cnt]) => `<button type="button" data-f="${k}" class="${clipFilter === k ? "on" : ""}">${n} <small>${cnt}</small></button>`).join("")}</div>
      <span class="spacer"></span>${all.length ? `<button class="btn sm ghost danger" id="t-clear">Удалить все</button>` : ""}</div>
    <div class="clips">${shown.slice(0, clipLimit).map((x) => {
      const k = x.kind + "/" + x.name, playing = LIST.key === k && LIST.audio && !LIST.audio.paused;
      return `<div class="clip${playing ? " on" : ""}" data-key="${esc(k)}">
        <button class="ib clip-play" data-play="${esc(k)}" aria-label="Прослушать">${icon(playing ? "pause" : "play", "solid")}</button>
        <span class="clip-meta"><b>${esc(clipWhen(x))}${x.remote ? ` <span class="on-cloud" data-tip="На Google Диске — колонка докачает при прослушивании">${icon("cloud")}</span>` : ""}</b><small>${clipSec(x).toFixed(1)} с${clipProb(x) ? ` · ${clipProb(x)}%` : ""}${x.reviewed ? " · проверено" : ""}</small></span>
        <div class="seg sm">${x.kind === "bg" ? `<button type="button" class="on" disabled>Фон</button>` : `<button type="button" data-mv="pos" data-k="${x.kind}" data-n="${esc(x.name)}" class="${x.kind === "pos" ? "on" : ""}">Джарвис</button><button type="button" data-mv="false" data-k="${x.kind}" data-n="${esc(x.name)}" class="${x.kind === "false" ? "on" : ""}">Не Джарвис</button>`}</div>
        <button class="ib clip-del" data-mv="delete" data-k="${x.kind}" data-n="${esc(x.name)}" aria-label="Удалить">${icon("x")}</button>
      </div>`; }).join("") || `<p class="note">Здесь пока пусто</p>`}</div>
    ${shown.length > clipLimit ? `<button class="btn sm ghost" id="cl-more">Показать ещё ${Math.min(40, shown.length - clipLimit)} из ${shown.length - clipLimit}</button>` : ""}`;
  box.onclick = async (e) => {
    const f = e.target.closest("[data-f]"), mv = e.target.closest("[data-mv]"), pl = e.target.closest("[data-play]");
    if (f) { clipFilter = f.dataset.f; clipLimit = 40; drawClips(); return; }
    if (e.target.closest("#cl-more")) { clipLimit += 40; drawClips(); return; }
    if (e.target.closest("#t-clear")) { if (confirm("Удалить все записи для обучения? Сначала лучше скачать архив.")) act(null, async () => { await api("DELETE", TRAIN); drawTrainingRecords(); }, "Записи удалены"); return; }
    if (pl) {
      const a = LIST.audio ||= new Audio();
      if (LIST.key === pl.dataset.play && !a.paused) a.pause();
      else { const [kind, ...n] = pl.dataset.play.split("/"); if (LIST.key !== pl.dataset.play) a.src = clipUrl({ kind, name: n.join("/") }); LIST.key = pl.dataset.play; a.play().catch(() => {}); }
      a.onended = a.onpause = a.onplay = () => drawClips();
      return;
    }
    if (mv && !mv.classList.contains("on")) await act(mv, async () => {
      await api("POST", TRAIN + "/move", { kind: mv.dataset.k, name: mv.dataset.n, to: mv.dataset.mv, reviewed: true });
      T = await api("GET", TRAIN); T.clips ||= []; drawClips(); drawReview();
    });
  };
}
