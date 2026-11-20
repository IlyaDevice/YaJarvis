// Каркас: вход, состояние с колонки, опрос, сайдбар, маршруты, запуск.
import { viewProfile } from "./assistant.js";
import { viewCharacters } from "./characters.js";
import { ST, navPill, statsData } from "./charts.js";
import { viewConnections } from "./connections.js";
import { $, $$, act, api, dirty, esc, icon, ICONS, ring, setDirty } from "./core.js";
import { homeData, viewHome } from "./home.js";
import { HL, homeLogs } from "./logs.js";
import { drawVolume, musicData, viewPlayer } from "./player.js";
import { viewMemory } from "./memory.js";
import { viewSpeaker } from "./speaker.js";
import { wizOpen } from "./wizard.js";
import { drawWakeTraining } from "./wake-training.js";

export let S = null;          // состояние с колонки
export let journal = [];
export let page = "home";
export let pollTimer = null;
let svelteView;

const svelteViews = new Map([
  ["charts", async () => (await import("../Charts/mountCharts.js")).mountCharts],
  ["logs", async () => (await import("../Logs/mountLogs.js")).mountLogs],
  ["security", async () => (await import("../Security/mountSecurity.js")).mountSecurity],
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

// Кольцо перерисовываем, только когда состояние сменилось, — иначе анимация дёргается на каждом опросе.
export function setRing(box, state, cls) {
  if (!box || box.dataset.state === state) return;
  box.dataset.state = state;
  box.innerHTML = ring(state, cls);
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
export const engineName = (p) => !p.engine ? "Голос не настроен" : !voiceEnabled(p.engine) ? "Голосовой плагин выключен" :
  ({ openai: "GPT-Live", live: "GPT-Live", gemini: "Gemini Live", chatgpt: "GPT-Live по подписке", grok: "Grok Voice", elevenlabs: "ElevenLabs", eleven_realtime: "ElevenLabs Live" })[p.engine] || "Голосовой плагин";

export function renderSide() {
  $("#upd-dot")?.classList.toggle("hidden", !S.update_available);
  $("#profiles").innerHTML = S.profiles.map((p) => `<div class="prof${page === "p:" + p.id ? " on" : ""}" draggable="true" data-id="${esc(p.id)}">
    ${avatar(p)}<div class="meta"><div class="n">${esc(p.name)}</div><div class="s">${engineName(p)}${voiceEnabled(p.engine) && p.voice ? ` · ${esc(voiceName(p.voice))}` : ""}</div></div>
    ${p.id === S.active ? `<span class="act" title="Активный профиль"></span>` : ""}</div>`).join("");
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
  if (dirty && !confirm("Есть несохранённые изменения. Уйти без сохранения?")) { history.back(); return; }
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
  const h = location.hash.slice(1) || "home";
  const loadSvelteView = svelteViews.get(h);
  const v0 = $("#view"); v0.oninput = v0.onchange = v0.onclick = null;   // обработчики прошлой страницы
  if (h.startsWith("p/")) { page = "p:" + h.slice(2); viewProfile(h.slice(2)); }
  else if (h.startsWith("connections/")) { page = "connections"; viewConnections(h.slice(12)); }
  else if (h === "plugins") { location.replace("#connections/plugins"); return; } // страницы «Плагины» больше нет: плагины — в «Подключениях»
  else if (loadSvelteView) {
    page = h;
    void viewSvelte(v0, loadSvelteView);
  }
  else if (h === "training") {
    page = "training";
    v0.innerHTML = '<header class="ph"><h1>Обучение</h1></header>';
    if (S.settings.wake_training) {
      v0.insertAdjacentHTML("beforeend", '<div id="wake-training"></div>');
      void drawWakeTraining(v0.querySelector("#wake-training"));
      if (S.settings.training_records) {
        v0.insertAdjacentHTML("beforeend", '<div id="training-records"></div>');
        const records = v0.querySelector("#training-records");
        void import("./training.js").then((m) => m.drawTrainingRecords(records)).catch((error) => {
          if (records.isConnected) records.textContent = error.message;
        });
      }
    } else {
      v0.insertAdjacentHTML("beforeend", '<p class="note">Включите плагин «Обучение слова» в <a href="#connections/training">подключениях</a>.</p>');
    }
  }
  else {
    page = ["home", "player", "characters", "memory", "connections", "speaker"].includes(h) ? h : "home";
    ({ home: viewHome, player: viewPlayer, characters: viewCharacters, memory: viewMemory, connections: viewConnections, speaker: viewSpeaker })[page]();
  }
  renderSide();
  window.scrollTo(0, 0);
  const v = $("#view");          // одно появление на переход между страницами
  v.classList.remove("enter"); void v.offsetWidth; v.classList.add("enter");
}

// ---------------------------------------------------------------- запуск
start().catch(() => {});
