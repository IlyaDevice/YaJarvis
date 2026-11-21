// Каркас: вход, состояние с колонки, опрос, сайдбар, маршруты, запуск.
import { ST, navPill, statsData } from "./charts.js";
import { $, $$, act, api, dirty, esc, icon, ICONS, setDirty } from "./core.js";
import { homeData, viewHome } from "./home.js";
import { HL, homeLogs } from "./logs.js";
import { drawVolume, musicData } from "./player.js";
import { wizOpen } from "./wizard.js";

export let S = null;          // состояние с колонки
export let journal = [];
export let page = "home";
export let pollTimer = null;
let svelteView;
let routedHash;

const svelteViews = new Map([
  ["characters", async () => (await import("../Characters/mountCharacters.js")).mountCharacters],
  ["charts", async () => (await import("../Charts/mountCharts.js")).mountCharts],
  ["logs", async () => (await import("../Logs/mountLogs.js")).mountLogs],
  ["memory", async () => (await import("../Memory/mountMemory.js")).mountMemory],
  ["player", async () => (await import("../Player/mountPlayer.js")).mountPlayer],
  ["security", async () => (await import("../Security/mountSecurity.js")).mountSecurity],
  ["speaker", async () => (await import("../Speaker/mountSpeaker.js")).mountSpeaker],
]);

// ---------------------------------------------------------------- вход
export let gateSetup = false;
export function showGate(setup) {
  gateSetup = !!setup;
  closeSvelteView();
  $("#app").classList.add("hidden");
  $("#gate").classList.remove("hidden");
  $("#gate-text").textContent = setup ? "Придумайте пароль для настроек колонки. Он понадобится, чтобы зайти сюда снова." : "Пароль от настроек колонки";
  $("#gate-pass2").classList.toggle("hidden", !setup);
  $("#gate-forgot").classList.toggle("hidden", setup);
  $("#gate-btn").textContent = setup ? "Сохранить пароль" : "Войти";
  $("#gate-pass").autocomplete = setup ? "new-password" : "current-password";
  $("#gate-pass").focus();
  clearInterval(pollTimer);
}

$("#gate-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const p = $("#gate-pass").value;
  $("#gate-err").textContent = "";
  try {
    if (gateSetup) {
      if (p !== $("#gate-pass2").value) throw new Error("Пароли не совпадают");
      await api("POST", "/api/setup", { password: p });
    } else {
      await api("POST", "/api/login", { password: p });
    }
    $("#gate-pass").value = $("#gate-pass2").value = "";
    $("#gate").classList.add("hidden");
    await start();
  } catch (err) { $("#gate").classList.remove("hidden"); $("#gate-err").textContent = err.message; }
});

$("#logout").addEventListener("click", async () => { await api("POST", "/api/logout").catch(() => {}); showGate(false); });
// меню на телефоне: шторка слева с затемнением; закрывается тапом мимо, Escape и переходом на страницу
export const scrim = document.createElement("div");
scrim.className = "scrim";
document.body.append(scrim);
export const setMenu = (open) => { $("#side").classList.toggle("open", open); document.body.classList.toggle("menu-open", open); };
$("#menu").addEventListener("click", () => setMenu(!$("#side").classList.contains("open")));
scrim.addEventListener("click", () => setMenu(false));
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && $("#side").classList.contains("open")) setMenu(false); });

// ---------------------------------------------------------------- данные
export async function reload() {
  S = await api("GET", "/api/state");
  S.live_voices ||= []; S.prompts ||= []; S.profiles ||= []; S.security ||= [];
  S.gemini_models ||= []; S.gemini_voice_groups ||= []; S.chatgpt_voice_groups ||= []; S.grok_voice_groups ||= []; S.chatgpt ||= {}; S.sys ||= {};
  renderSide();
}

export async function start() {
  await reload();
  $("#app").classList.remove("hidden");
  $("#ver").textContent = "v" + S.version;
  route();
  if (!S.settings.onboarded) wizOpen(0);
  clearInterval(pollTimer);
  pollTimer = setInterval(poll, 2000);
  poll();
}

export async function poll() {
  const refreshView = svelteView?.refresh;
  let d;
  try { d = await api("GET", "/api/journal"); } catch { return; }
  if (d.version && S.version && d.version !== S.version) { location.reload(); return; }   // колонку обновили — берём новую панель
  journal = d.items || [];
  S.speaker.mic = d.mic; S.speaker.mic_why = d.mic_why || ""; S.speaker.talking = d.talking; S.speaker.phase = d.phase || ""; S.sys = d.sys || {};
  renderStatus();
  if (d.volume >= 0) S.speaker.volume = d.volume;
  if (page === "home") { homeData(); drawVolume(); if (Date.now() - ST.at > 15000) statsData(); if (Date.now() - HL.at > 5000) homeLogs(); }
  if (page === "player") drawVolume();
  if (refreshView && refreshView === svelteView?.refresh) void refreshView();
  musicData();
  clearTimeout(poll.fast);
  if (S.speaker.talking && page === "home") poll.fast = setTimeout(poll, 500);
}

// Что сейчас делает колонка: вид кольца, заголовок и пояснение.
export function speakerState() {
  const sp = S.speaker, word = S.settings.wake_word || "Джарвис";
  if (sp.talking) return { ring: "talk", title: "Идёт разговор", text: "Говорите — пока разговор открыт, звать по имени не нужно" };
  if (!sp.mic) return { ring: "off", title: "Микрофон не подключён", text: sp.mic_why ? `Колонка не присылает звук: ${sp.mic_why}. Подробности — в «Журнале».` : "Колонка не присылает звук. Проверьте, что она включена и установка завершилась." };
  if (sp.wake_model && S.settings.wake_enabled) return { ring: "idle", title: `Жду фразу «${word}»`, text: `Скажите «${word}, …» или нажмите «Поговорить»` };
  return { ring: "mute", title: "Жду кнопку «Поговорить»", text: S.settings.wake_enabled ? `Модель для «${word}» ещё не установлена` : `Отклик на «${word}» выключен` };
}

export const ORB_TEXT = { idle: "Колонка ждёт фразу активации", listening: "Слушает", thinking: "Думает", speaking: "Говорит", off: "Нет звука с колонки", mute: "На связи, голосовая активация недоступна" };
export function renderStatus() {
  const st = speakerState(), ph = S.speaker.phase;
  const o = st.ring === "off" ? "off" : S.speaker.talking ? (ph || "listening") : st.ring === "mute" ? "mute" : "idle";
  const orb = $("#orb");
  if (orb.dataset.s !== o) { orb.dataset.s = o; orb.setAttribute("aria-label", ORB_TEXT[o]); orb.title = ORB_TEXT[o]; }
}

// ---------------------------------------------------------------- сайдбар
export const avatar = (p, big = false) => `<span class="av c${p.color || 1}${big ? " big" : ""}">${esc((p.name || "?").trim()[0] || "?")}</span>`;
export const voiceName = (id) => (S.live_voices.find((v) => v.id === id)?.name) || id || "cedar";
export const voicePlugin = (engine) => ({ live: "openai", eleven_realtime: "elevenlabs" })[engine] || engine;
export const voiceEnabled = (engine) => !!engine && !!S.plugins?.enabled?.includes(voicePlugin(engine));
export const isClassicProfile = (p) => !!p && p.engine !== "eleven_realtime" && (p.mode === "classic" || p.engine === "elevenlabs" || p.engine === "classic");
export const sttReady = (id) => ["openai", "elevenlabs"].includes(id) ? voiceEnabled(id) : (S.stt_plugins || []).some((x) => x.id === id);
export const ttsReady = (id) => ["openai", "elevenlabs"].includes(id) ? voiceEnabled(id) : (S.tts_plugins || []).some((x) => x.id === id);
export const classicEnabled = () =>
  (voiceEnabled("openai") || voiceEnabled("elevenlabs") || (S.stt_plugins || []).length > 0)
  && (voiceEnabled("openai") || voiceEnabled("elevenlabs") || (S.tts_plugins || []).length > 0);
export const engineName = (p) => {
  if (isClassicProfile(p)) {
    const stt = p.stt_engine || "elevenlabs";
    const tts = p.tts_engine || (p.engine === "openai" ? "openai" : "elevenlabs");
    if (!sttReady(stt) || !ttsReady(tts)) return "Плагин пайплайна выключен";
    return stt === "elevenlabs" && tts === "elevenlabs" ? "ElevenLabs" : "Пайплайн";
  }
  if (!p.engine) return "Голос не настроен";
  if (!voiceEnabled(p.engine)) return "Голосовой плагин выключен";
  return ({ openai: "GPT-Live", live: "GPT-Live", gemini: "Gemini Live", chatgpt: "GPT-Live по подписке", grok: "Grok Voice", elevenlabs: "ElevenLabs", eleven_realtime: "ElevenLabs Live" })[p.engine] || "Голосовой плагин";
};

export function renderSide() {
  $("#upd-dot")?.classList.toggle("hidden", !S.update_available);
  $("#profiles").innerHTML = S.profiles.map((p) => {
    const classic = isClassicProfile(p);
    const tts = p.tts_engine || (p.engine === "openai" ? "openai" : "elevenlabs");
    const sideVoice = classic ? (ttsReady(tts) && tts === "openai" && p.voice ? voiceName(p.voice) : "") : (voiceEnabled(p.engine) && p.voice ? voiceName(p.voice) : "");
    return `<div class="prof${page === "p:" + p.id ? " on" : ""}" draggable="true" data-id="${esc(p.id)}">
    ${avatar(p)}<div class="meta"><div class="n">${esc(p.name)}</div><div class="s">${engineName(p)}${sideVoice ? ` · ${esc(sideVoice)}` : ""}</div></div>
    ${p.id === S.active ? `<span class="act" title="Активный профиль"></span>` : ""}</div>`;
  }).join("");
  $$("#profiles .prof").forEach((el) => {
    el.addEventListener("click", () => go("#p/" + el.dataset.id));
    el.addEventListener("dragstart", (e) => { el.classList.add("drag"); e.dataTransfer.setData("text/plain", el.dataset.id); });
    el.addEventListener("dragend", () => el.classList.remove("drag"));
    el.addEventListener("dragover", (e) => e.preventDefault());
    el.addEventListener("drop", async (e) => {
      e.preventDefault();
      const from = e.dataTransfer.getData("text/plain"), to = el.dataset.id;
      if (from === to) return;
      const ids = S.profiles.map((p) => p.id).filter((id) => id !== from);
      ids.splice(ids.indexOf(to), 0, from);
      await act(null, async () => { await api("POST", "/api/profiles/order", { ids }); await reload(); });
    });
  });
  // страницы плагинов — в меню «Колонка», только пока плагин стоит и работает
  const training = S.settings.wake_training ? `<a href="#training" data-page="training">${icon("mic")}Обучение</a>` : "";
  if ($("#nav-ext").innerHTML !== training) $("#nav-ext").innerHTML = training;
  $$(".nav a").forEach((a) => a.classList.toggle("on", a.dataset.page === page));
  navPill();
}

$("#add-profile").addEventListener("click", () => go("#p/new"));

// ---------------------------------------------------------------- маршруты
export function go(hash) { location.hash = hash; }
window.addEventListener("hashchange", () => {
  if (!S || $("#app").classList.contains("hidden")) return;
  if (dirty && !confirm("Есть несохранённые изменения. Уйти без сохранения?")) {
    history.replaceState(history.state, "", routedHash || location.pathname + location.search);
    return;
  }
  route();
});
window.addEventListener("beforeunload", (e) => { if (dirty) e.preventDefault(); });

async function viewSvelte(target, load) {
  target.replaceChildren();
  const view = {};
  svelteView = view;
  try {
    const mount = await load();
    if (svelteView === view) Object.assign(view, mount(target));
  } catch (error) {
    if (svelteView === view) target.textContent = error.message;
  }
}

function closeSvelteView() {
  const view = svelteView;
  svelteView = null;
  void view?.dispose?.();
}

export function route() {
  closeSvelteView();
  setDirty(false);
  $("#side").classList.remove("open");
  document.body.classList.remove("menu-open");
  routedHash = location.hash;
  const h = routedHash.slice(1) || "home";
  const loadSvelteView = svelteViews.get(h);
  const v0 = $("#view"); v0.oninput = v0.onchange = v0.onclick = null;   // обработчики прошлой страницы
  if (h.startsWith("p/")) {
    const id = h.slice(2);
    page = "p:" + id;
    void viewSvelte(v0, async () => {
      const { mountProfile } = await import("../Profile/mountProfile.js");
      return (target) => mountProfile(target, id);
    });
  }
  else if (h === "connections" || h.startsWith("connections/")) {
    page = "connections";
    void viewSvelte(v0, async () => {
      const { mountConnections } = await import("../Connections/mountConnections.js");
      return (target) => mountConnections(target, h.slice(12));
    });
  }
  else if (h === "plugins") { location.replace("#connections/plugins"); return; } // страницы «Плагины» больше нет: плагины — в «Подключениях»
  else if (loadSvelteView) {
    page = h;
    void viewSvelte(v0, loadSvelteView);
  }
  else if (h === "training") {
    page = "training";
    v0.innerHTML = '<header class="ph"><h1>Обучение</h1></header>';
    if (S.settings.wake_training) {
      const training = v0.appendChild(document.createElement("div"));
      const showRecords = Boolean(S.settings.training_records);
      void viewSvelte(training, async () => {
        const { mountTraining } = await import("../Training/mountTraining.js");
        return (target) => mountTraining(target, showRecords);
      });
    } else {
      v0.insertAdjacentHTML("beforeend", '<p class="note">Включите плагин «Обучение слова» в <a href="#connections/training">подключениях</a>.</p>');
    }
  }
  else {
    page = "home";
    const chat = viewHome();
    void viewSvelte(chat, async () => (await import("../HomeChat/mountHomeChat.js")).mountHomeChat);
  }
  renderSide();
  window.scrollTo(0, 0);
  const v = $("#view");          // одно появление на переход между страницами
  v.classList.remove("enter"); void v.offsetWidth; v.classList.add("enter");
}

// ---------------------------------------------------------------- запуск
start().catch(() => {});
