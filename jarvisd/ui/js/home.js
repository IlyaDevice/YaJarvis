// Главная.
import { KPI, ST, drawKpis, kpiHtml, statsData } from "./charts.js";
import { $, esc, icon, tip } from "./core.js";
import { homeLogsSetup } from "./logs.js";
import { S } from "./main.js";
import { M, drawPlayer, drawVolume, musicData, playerHtml, playerSetup } from "./player.js";

// ---------------------------------------------------------------- главная
export function setupSteps() {
  return [
    { done: !!S.keys.openai || !!S.keys.gemini || !!S.keys.grok || !!S.chatgpt.signed_in || (S.text_plugins || []).some((plugin) => S.profiles.find((p) => p.id === S.active)?.llm_plugin === plugin.id), title: "Подключить модель", text: "Выберите готовый сервис или установите свой текстовый плагин", href: "#connections" },
    { done: S.vless.set && !S.vless.error, title: "VPN (ссылка VLESS)", text: "OpenAI не работает напрямую из России и Беларуси", href: "#connections" },
    { done: !!S.keys.yandex || S.settings.home_via !== "jarvis", title: "Умный дом", text: "Через Алису работает сразу; для режима «Джарвис» нужен токен Яндекса", href: "#speaker" },
    { done: !!S.settings.city, title: "Ваш город", text: "Для погоды и новостей без уточнений", href: "#speaker" },
  ];
}

export function viewHome() {
  const steps = setupSteps(), left = steps.filter((x) => !x.done), doneN = steps.length - left.length;
  $("#view").innerHTML = `<header class="ph ph-row"><div><h1>Главная</h1><p class="sub"><span class="dot" id="home-dot"></span><span id="home-sub"></span></p></div></header>
  <div class="kpis stagger" id="kpis">${KPI.map(kpiHtml).join("")}</div>
  <div class="home">
  <section class="tg" id="home-chat" aria-label="Переписка с ассистентом"></section>
  <aside class="rail">
    ${left.length ? `<div class="card"><h2>Осталось настроить <span class="count">${doneN} из ${steps.length}</span></h2>
      <progress class="bar" value="${doneN}" max="${steps.length}"></progress>
      ${steps.map((x) => `<a class="step${x.done ? " done" : ""}" href="${x.href}">${icon(x.done ? "done" : "todo")}<span class="sb"><b>${esc(x.title)}</b><small>${esc(x.text)}</small></span>${x.done ? "" : icon("right")}</a>`).join("")}</div>` : ""}
    <div class="card"><h2>Состояние</h2><dl class="stat">
      <div><dt>Микрофон ${tip("Звук с микрофонов колонки после шумо- и эхоподавления Яндекса.")}</dt><dd id="t-mic"></dd></div>
      <div><dt>Фраза «${esc(S.settings.wake_word || "Джарвис")}» ${tip("Распознаётся прямо на колонке маленькой нейросетью — без интернета и сразу.")}</dt><dd id="t-wake"></dd></div>
      <div><dt>Интернет ${tip("Как колонка ходит в OpenAI: через ваш VPN (VLESS) или напрямую.")}</dt><dd id="t-net"></dd></div>
      <div><dt>Свободно на диске ${tip("На флешке колонки мало места. Если свободно меньше 5 МБ, обновления могут не встать.")}</dt><dd id="t-disk"></dd></div>
    </dl></div>
    <section class="card github-support" id="github-support" aria-label="Поддержать проект">
      <button class="github-close" id="github-dismiss" type="button" aria-label="Больше не показывать просьбу поддержать проект" title="Больше не показывать">${icon("x")}</button>
      <span class="github-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 19c-4.3 1.3-4.3-2.2-6-2.6M15 22v-3.9a3.4 3.4 0 0 0-.9-2.6c3-.3 6.2-1.5 6.2-6.9a5.4 5.4 0 0 0-1.5-3.7 5 5 0 0 0-.1-3.7S17.5.8 15 2.6a12.8 12.8 0 0 0-6 0C6.5.8 5.3 1.2 5.3 1.2a5 5 0 0 0-.1 3.7 5.4 5.4 0 0 0-1.5 3.7c0 5.4 3.2 6.6 6.2 6.9a3.4 3.4 0 0 0-.9 2.6V22"/></svg></span>
      <div class="github-copy"><h2>Поддержать YaJavis</h2><p>Нравится проект? Оставьте звезду на GitHub.</p>
        <a class="github-link" href="https://github.com/newfpv/YaJavis" target="_blank" rel="noopener noreferrer">Поставить звезду ${icon("ext")}</a>
      </div>
    </section>
    ${playerHtml()}
    <section class="card home-logs tight" id="hlogs"><div class="card-head"><h2>Логи</h2><a class="meta hl-all" href="#logs">Все логи ${icon("right")}</a></div><div class="logbox" id="hlogbox"></div></section>
  </aside>
  </div>`;
  ST.at = 0; drawKpis(); statsData();
  playerSetup(); drawVolume(); if (M) drawPlayer(); musicData();
  homeLogsSetup();
  try { if (localStorage.getItem("yajavis.star.dismissed") === "1") $("#github-support")?.remove(); } catch {}
  $("#github-dismiss")?.addEventListener("click", () => {
    $("#github-support")?.remove();
    try { localStorage.setItem("yajavis.star.dismissed", "1"); } catch {}
  });
  homeData();
  return $("#home-chat");
}

export function homeData() {
  if (!$("#home-sub")) return;
  const sys = S.sys || {};
  drawKpis();
  const up = sys.uptime_s || 0, d = Math.floor(up / 86400), h = Math.floor((up % 86400) / 3600), m = Math.floor((up % 3600) / 60);
  const hd = "dot " + (S.speaker.mic ? "on" : "off");
  if ($("#home-dot").className !== hd) $("#home-dot").className = hd;
  $("#home-sub").textContent = `${S.speaker.model || "Яндекс Станция"} · Джарвис ${S.version}${up ? ` · колонка работает ${d ? `${d} д ${h} ч` : h ? `${h} ч ${m} мин` : `${m} мин`}` : ""}`;
  const disk = $("#t-disk");
  disk.textContent = sys.disk_total_mb ? `${sys.disk_free_mb} из ${sys.disk_total_mb} МБ` : "—";
  disk.className = "v " + (sys.disk_total_mb && sys.disk_free_mb < 5 ? "warn" : "ok");
  const set = (id, text, cls) => { const el = $(id); el.textContent = text; el.className = "v " + cls; };
  set("#t-mic", S.speaker.mic ? "на связи" : "нет звука", S.speaker.mic ? "ok" : "bad");
  set("#t-wake", !S.settings.wake_enabled ? "выключено" : S.speaker.wake_model ? "слушаю" : "нет модели", !S.settings.wake_enabled ? "warn" : S.speaker.wake_model ? "ok" : "warn");
  set("#t-net", S.vless.error ? "ошибка VPN" : S.vless.set ? "через VPN" : "напрямую", S.vless.error ? "bad" : S.vless.set ? "ok" : "warn");
}
