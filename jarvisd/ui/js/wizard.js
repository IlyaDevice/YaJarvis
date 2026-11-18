// Мастер первого запуска. Он предлагает только пакеты из официального каталога.
import { connClose, connOpen } from "./connections.js";
import { $, api, esc, icon, toast } from "./core.js";
import { S, go, reload, voicePlugin } from "./main.js";
import { PL, plCatalog, plDescription, plIcon, plLoad } from "./plugins.js";

const STEPS = ["Начало", "Плагины", "Настройка", "Готово"];
const groups = [
  ["assistant", "Голосовые ассистенты"],
  ["home", "Умный дом"],
  ["media", "Музыка"],
  ["system", "Колонка и сеть"],
  ["info", "Справки"],
  ["fun", "Игры"],
];
const voiceIds = ["chatgpt", "gemini", "openai", "grok", "elevenlabs"];
const serviceIds = { yandexhome: "yandex" };
const hasService = new Set([...voiceIds, "vpn", "youtube", "yandexhome", "homeassistant", "smartlife", "xiaomi"]);
const picked = new Set();
const failed = new Map();
let step = 0, busy = false, error = "";

function installed() {
  return new Map((PL.data?.installed || []).map((p) => [p.id, p]));
}

function ready(id) {
  if (id === "chatgpt") return S.chatgpt?.signed_in;
  return !!S.keys?.[id];
}

export function wizOpen(at = 0) {
  step = at;
  $("#wiz-pill")?.remove();
  let w = $("#wiz");
  if (!w) {
    w = document.createElement("div");
    w.id = "wiz";
    w.className = "wiz";
    w.setAttribute("role", "dialog");
    w.setAttribute("aria-modal", "true");
    document.body.append(w);
    w.addEventListener("click", click);
    w.addEventListener("change", (event) => {
      const id = event.target.dataset.wzPick;
      if (id) {
        picked[event.target.checked ? "add" : "delete"](id);
        failed.delete(id);
        const why = event.target.closest("[data-wz-row]")?.querySelector("[data-wz-row-error]");
        if (why) why.classList.add("hidden");
        updateControls();
      }
    });
  }
  w.classList.remove("hidden");
  draw();
}

async function refreshCatalog(fresh = false) {
  await Promise.all([plCatalog(fresh), plLoad()]);
  if (step === 1) draw();
}

function options() {
  const have = installed();
  const catalog = (PL.list || []).filter((item) => item.official);
  if (PL.loading) return '<p role="status">Загружаем официальный каталог…</p>';
  if (!catalog.length) return '<p role="alert">Каталог пока недоступен. Проверьте, есть ли у колонки интернет.</p><button class="btn" type="button" data-wz-retry>Обновить каталог</button>';
  return groups.map(([category, name]) => {
    const items = catalog.filter((item) => item.category === category);
    if (!items.length) return "";
    return `<h3>${name}</h3><div class="wz-list">${items.map((item) => {
      const current = have.get(item.id);
      const status = current?.running ? "установлен" : current ? "выключен" : "";
      const why = failed.get(item.id);
      return `<label class="wz-ai${current ? " on" : ""}" data-wz-row="${esc(item.id)}">${plIcon(item.icon)}<div><b>${esc(item.name)}</b><span>${esc(item.description)}</span>
        <span class="err${why ? "" : " hidden"}" data-wz-row-error role="alert">${esc(why || "")}</span></div>
        <span class="st${status ? "" : " hidden"}" data-wz-row-status role="status">${status}</span>
        ${current ? "" : `<span class="sw" data-wz-switch><input type="checkbox" data-wz-pick="${esc(item.id)}" ${picked.has(item.id) ? "checked" : ""} aria-label="${esc(item.name)}"><span class="tr"></span></span>`}</label>`;
    }).join("")}</div>`;
  }).join("");
}

function configuration() {
  const have = [...installed().values()].filter((p) => p.official && (hasService.has(p.id) || p.id === "training"));
  have.sort((a, b) => Number(b.id === "vpn") - Number(a.id === "vpn"));
  const configured = have.filter((p) => p.running && voiceIds.includes(p.id) && ready(p.id));
  const current = S.profiles?.find((p) => p.id === S.active)?.engine;
  const selected = configured.find((p) => p.id === voicePlugin(current))?.id || configured[0]?.id;
  return `<h1>Настройте подключения</h1><p>Откройте нужный плагин и следуйте инструкции в его настройках. Если потребуется VPN, начните с него.</p>
    ${have.length ? `<div class="wz-list">${have.map((p) => `<div class="wz-ai">${plIcon(p.icon)}<div><b>${esc(p.name)}</b><span>${esc(plDescription(p))}</span></div>
      <button class="btn sm" type="button" data-wz-conn="${esc(p.id)}">${ready(p.id) ? "Открыть" : "Настроить"}</button></div>`).join("")}</div>`
      : '<p class="muted">Подключения можно добавить позже. Чтобы Джарвис отвечал голосом, выберите и настройте голосового ассистента.</p>'}
    ${configured.length ? `<div class="f"><label for="wz-voice">Кто будет отвечать</label><select id="wz-voice">${configured.map((p) => {
      const engine = p.id === "elevenlabs" && current !== "elevenlabs" ? "eleven_realtime" : p.id;
      return `<option value="${esc(engine)}"${p.id === selected ? " selected" : ""}>${esc(p.name)}</option>`;
    }).join("")}</select></div>` : ""}`;
}

function draw() {
  const w = $("#wiz");
  if (!w || w.classList.contains("hidden")) return;
  let card = w.querySelector(".wiz-card");
  if (!card) {
    card = document.createElement("div");
    card.className = "wiz-card";
    w.append(card);
  }
  const view = [
    () => `<div class="wz-hero"><span class="orb big" data-s="idle"><i></i></span><h1>Привет! Я Джарвис</h1>
      <p>Сейчас на колонке только базовые функции. Выберите, чем будете пользоваться: голосом, музыкой, умным домом или своими командами.</p></div>`,
    () => `<h1>Выберите плагины</h1><p>Поставьте один голосовой плагин, если хотите разговаривать с Джарвисом. Всё остальное можно добавить или удалить позже в «Подключениях».</p>${options()}`,
    configuration,
    () => `<div class="wz-hero"><span class="orb big" data-s="idle"><i></i></span><h1>Готово</h1>
      <p>Если настроили голосовой плагин, скажите <b>«Джарвис, привет»</b>. Добавить и удалить плагины можно в «Подключениях».</p>
      <p class="muted">Мастер можно открыть снова в разделе «Безопасность».</p></div>`,
  ];
  card.innerHTML = `<div class="wz-top"><nav class="wz-dots">${STEPS.map((name, i) => `<span class="wz-dot${step === i ? " on" : i < step ? " done" : ""}"${step === i ? ' aria-current="step"' : ""}><i></i><span>${name}</span></span>`).join("")}</nav>
      <button class="btn ghost sm" type="button" data-wz-skip>Пропустить</button></div>
    <div class="wz-body">${view[step]()}<p class="err hidden" data-wz-error role="alert"></p></div>
    <div class="wz-foot">${step > 0 ? '<button class="btn ghost" type="button" data-wz-back>Назад</button>' : ""}<span class="spacer"></span>
      ${step === 1 ? '<button class="btn ghost hidden" type="button" data-wz-later>Продолжить без них</button>' : ""}
      <button class="btn primary" type="button" data-wz-next></button>
    </div>`;
  updateControls();
}

function updateControls() {
  const w = $("#wiz");
  const next = w?.querySelector("[data-wz-next]");
  if (!next) return;
  const have = installed();
  const selected = step === 1 && [...picked].some((id) => !have.has(id));
  next.textContent = busy && step === 1 ? "Устанавливаем…" : step === 3 ? "Начать" : selected ? "Установить выбранные" : "Дальше";
  w.querySelector("[data-wz-later]")?.classList.toggle("hidden", !selected || busy);
  const message = w.querySelector("[data-wz-error]");
  message.textContent = error;
  message.classList.toggle("hidden", !error);
  w.querySelectorAll("button, [data-wz-pick]").forEach((control) => { control.disabled = busy; });
}

function updateRow(id, status, message = "", installed = false) {
  const row = $("#wiz").querySelector(`[data-wz-row="${CSS.escape(id)}"]`);
  if (!row) return;
  const state = row.querySelector("[data-wz-row-status]");
  const why = row.querySelector("[data-wz-row-error]");
  state.textContent = status;
  state.classList.toggle("hidden", !status);
  why.textContent = message;
  why.classList.toggle("hidden", !message);
  row.classList.toggle("on", installed);
  row.querySelector("[data-wz-switch]")?.classList.toggle("hidden", !!status);
}

async function install() {
  const have = installed();
  const catalog = new Map((PL.list || []).filter((item) => item.official).map((item) => [item.id, item]));
  const selected = [...picked].filter((id) => !have.has(id) && catalog.has(id));
  if (!selected.length) return true;
  for (const id of selected) {
    failed.delete(id);
    updateRow(id, "устанавливается…");
    try {
      await api("POST", "/api/plugins/install", { repo: catalog.get(id).repo || S.repo, id });
      picked.delete(id);
      updateRow(id, "установлен", "", true);
    } catch (err) {
      failed.set(id, err.message);
      updateRow(id, "", err.message);
    }
  }
  await Promise.all([reload(), plLoad()]);
  return !selected.some((id) => failed.has(id));
}

async function finish() {
  await api("POST", "/api/onboarded");
  S.settings.onboarded = true;
  $("#wiz")?.classList.add("hidden");
  $("#wiz-pill")?.remove();
}

function openConnection(id) {
  $("#wiz").classList.add("hidden");
  const pill = document.createElement("button");
  pill.id = "wiz-pill";
  pill.className = "btn primary wiz-pill";
  pill.type = "button";
  pill.innerHTML = `${icon("right")}Вернуться к настройке`;
  pill.onclick = async () => {
    try {
      await Promise.all([reload(), plLoad()]);
      connClose();
      wizOpen(2);
    } catch (err) { toast(err.message, true); }
  };
  document.body.append(pill);
  if (id === "training") go("#training");
  else {
    const key = serviceIds[id] || id;
    const hash = `#connections/${key}`;
    if (location.hash === hash) connOpen(key);
    else go(hash);
  }
}

async function click(event) {
  const button = event.target.closest("button");
  if (!button || busy) return;
  const previousStep = step;
  const d = button.dataset;
  error = "";
  try {
    if ("wzConn" in d) return openConnection(d.wzConn);
    if ("wzSkip" in d || ("wzNext" in d && step === 3)) {
      busy = true;
      updateControls();
      return await finish();
    }
    if ("wzBack" in d) { step--; return; }
    if ("wzLater" in d) { step++; return; }
    if ("wzRetry" in d) { const pending = refreshCatalog(true); draw(); return await pending; }
    if (!("wzNext" in d)) return;
    if (step === 1) {
      busy = true;
      updateControls();
      if (!await install()) { error = "Часть плагинов не установилась. Можно повторить или продолжить с теми, что уже поставлены."; return; }
    }
    if (step === 2) {
      const chosen = $("#wz-voice")?.value;
      const profile = S.profiles?.find((p) => p.id === S.active);
      if (chosen && profile && profile.engine !== chosen) {
        busy = true;
        updateControls();
        await api("PUT", "/api/profile", { ...profile, engine: chosen, mode: "live", voice: "", live_model: "" });
        await reload();
      }
    }
    step++;
    if (step === 1 && !PL.list) void refreshCatalog();
  } catch (err) {
    error = err.message;
  } finally {
    busy = false;
    if (previousStep !== step) {
      const card = $("#wiz")?.querySelector(".wiz-card");
      if (card) card.scrollTop = 0;
      draw();
    } else updateControls();
  }
}
