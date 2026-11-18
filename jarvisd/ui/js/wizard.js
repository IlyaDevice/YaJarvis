// Мастер первого запуска. Он предлагает только пакеты из официального каталога.
import { connOpen } from "./connections.js";
import { $, api, esc, icon, toast } from "./core.js";
import { S, go, reload } from "./main.js";
import { PL, plCatalog, plIcon, plLoad } from "./plugins.js";

const STEPS = ["Начало", "Плагины", "Настройка", "Готово"];
const groups = [
  ["assistant", "Голосовые ассистенты"],
  ["home", "Умный дом"],
  ["media", "Музыка"],
  ["system", "Колонка и сеть"],
  ["info", "Справки"],
  ["fun", "Игры"],
];
const voiceIds = ["chatgpt", "gemini", "openai", "grok"];
const serviceIds = { yandexhome: "yandex" };
const hasService = new Set([...voiceIds, "elevenlabs", "vpn", "youtube", "yandexhome", "homeassistant", "smartlife", "xiaomi"]);
const picked = new Set();
const failed = new Map();
let step = 0, busy = false, installing = "", error = "";

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
        draw();
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
      const status = current?.running ? "установлен" : current ? "выключен" : installing === item.id ? "устанавливается…" : "";
      const why = failed.get(item.id);
      return `<label class="wz-ai${current ? " on" : ""}">${plIcon(item.icon)}<div><b>${esc(item.name)}</b><span>${esc(item.description)}</span>
        ${why ? `<span class="err" role="alert">${esc(why)}</span>` : ""}</div>
        ${current || status ? `<span class="st">${status}</span>` : `<span class="sw"><input type="checkbox" data-wz-pick="${esc(item.id)}" ${picked.has(item.id) ? "checked" : ""} ${busy ? "disabled" : ""} aria-label="${esc(item.name)}"><span class="tr"></span></span>`}</label>`;
    }).join("")}</div>`;
  }).join("");
}

function configuration() {
  const have = [...installed().values()].filter((p) => p.official && (hasService.has(p.id) || p.id === "training"));
  have.sort((a, b) => Number(b.id === "vpn") - Number(a.id === "vpn"));
  const configured = have.filter((p) => p.running && voiceIds.includes(p.id) && ready(p.id));
  const current = S.profiles?.find((p) => p.id === S.active)?.engine;
  const selected = configured.find((p) => p.id === current)?.id || configured[0]?.id;
  return `<h1>Настройте подключения</h1><p>Откройте нужный плагин и следуйте инструкции в его настройках. Если потребуется VPN, начните с него.</p>
    ${have.length ? `<div class="wz-list">${have.map((p) => `<div class="wz-ai">${plIcon(p.icon)}<div><b>${esc(p.name)}</b><span>${esc(p.description)}</span></div>
      <button class="btn sm" type="button" data-wz-conn="${esc(p.id)}">${ready(p.id) ? "Открыть" : "Настроить"}</button></div>`).join("")}</div>`
      : '<p class="muted">Подключения можно добавить позже. Чтобы Джарвис отвечал голосом, выберите и настройте голосового ассистента.</p>'}
    ${configured.length ? `<div class="f"><label for="wz-voice">Кто будет отвечать</label><select id="wz-voice">${configured.map((p) => `<option value="${esc(p.id)}"${p.id === selected ? " selected" : ""}>${esc(p.name)}</option>`).join("")}</select></div>` : ""}`;
}

function draw() {
  const w = $("#wiz");
  if (!w || w.classList.contains("hidden")) return;
  const none = [...picked].every((id) => installed().has(id));
  const view = [
    () => `<div class="wz-hero"><span class="orb big" data-s="idle"><i></i></span><h1>Привет! Я Джарвис</h1>
      <p>Сейчас на колонке только базовые функции. Выберите, чем будете пользоваться: голосом, музыкой, умным домом или своими командами.</p></div>`,
    () => `<h1>Выберите плагины</h1><p>Поставьте один голосовой плагин, если хотите разговаривать с Джарвисом. Всё остальное можно добавить или удалить позже в «Подключениях».</p>${options()}`,
    configuration,
    () => `<div class="wz-hero"><span class="orb big" data-s="idle"><i></i></span><h1>Готово</h1>
      <p>Если настроили голосовой плагин, скажите <b>«Джарвис, привет»</b>. Добавить и удалить плагины можно в «Подключениях».</p>
      <p class="muted">Мастер можно открыть снова в разделе «Безопасность».</p></div>`,
  ];
  w.innerHTML = `<div class="wiz-card">
    <div class="wz-top"><nav class="wz-dots">${STEPS.map((name, i) => `<span class="wz-dot${step === i ? " on" : i < step ? " done" : ""}"${step === i ? ' aria-current="step"' : ""}><i></i><span>${name}</span></span>`).join("")}</nav>
      <button class="btn ghost sm" type="button" data-wz-skip ${busy ? "disabled" : ""}>Пропустить</button></div>
    <div class="wz-body">${view[step]()}${error ? `<p class="err" role="alert">${esc(error)}</p>` : ""}</div>
    <div class="wz-foot">${step > 0 ? `<button class="btn ghost" type="button" data-wz-back ${busy ? "disabled" : ""}>Назад</button>` : ""}<span class="spacer"></span>
      ${step === 1 && !none && !busy ? '<button class="btn ghost" type="button" data-wz-later>Продолжить без них</button>' : ""}
      <button class="btn primary" type="button" data-wz-next ${busy ? "disabled" : ""}>${busy ? "Устанавливаем…" : step === 3 ? "Начать" : step === 1 && !none ? "Установить выбранные" : "Дальше"}</button>
    </div></div>`;
}

async function install() {
  const have = installed();
  const catalog = new Map((PL.list || []).filter((item) => item.official).map((item) => [item.id, item]));
  const selected = [...picked].filter((id) => !have.has(id) && catalog.has(id));
  if (!selected.length) return true;
  for (const id of selected) {
    failed.delete(id);
    installing = id;
    const content = $(".wz-body");
    const top = content.scrollTop;
    draw();
    $(".wz-body").scrollTop = top;
    try {
      await api("POST", "/api/plugins/install", { repo: catalog.get(id).repo || S.repo, id });
      picked.delete(id);
    } catch (err) {
      failed.set(id, err.message);
    }
  }
  installing = "";
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
      wizOpen(2);
    } catch (err) { toast(err.message, true); }
  };
  document.body.append(pill);
  if (id === "training") go("#training");
  else {
    go("#connections");
    setTimeout(() => connOpen(serviceIds[id] || id), 120);
  }
}

async function click(event) {
  const button = event.target.closest("button");
  if (!button || busy) return;
  const d = button.dataset;
  error = "";
  try {
    if ("wzConn" in d) return openConnection(d.wzConn);
    if ("wzSkip" in d || ("wzNext" in d && step === 3)) return await finish();
    if ("wzBack" in d) { step--; return draw(); }
    if ("wzLater" in d) { step++; return draw(); }
    if ("wzRetry" in d) { const pending = refreshCatalog(true); draw(); return await pending; }
    if (!("wzNext" in d)) return;
    if (step === 1) {
      busy = true;
      if (!await install()) { error = "Часть плагинов не установилась. Можно повторить или продолжить с теми, что уже поставлены."; return; }
    }
    if (step === 2) {
      const chosen = $("#wz-voice")?.value;
      const profile = S.profiles?.find((p) => p.id === S.active);
      if (chosen && profile && profile.engine !== chosen) {
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
    draw();
  }
}
