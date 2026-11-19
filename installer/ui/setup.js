import "/token.js";
import { mount } from "svelte";
import { writable } from "svelte/store";
import DeviceCheck from "./DeviceCheck/DeviceCheck.svelte";
import InstallSettings from "./InstallSettings/InstallSettings.svelte";
import Showcase from "./Showcase/Showcase.svelte";
import SpeakerDiscovery from "./SpeakerDiscovery/SpeakerDiscovery.svelte";

const $ = (s) => document.querySelector(s);
const selectedSpeaker = writable(null);

async function api(method, url, body) {
  const r = await fetch(url, { method, headers: { "Content-Type": "application/json", "X-Token": window.TOKEN }, body: body ? JSON.stringify(body) : undefined });
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(d.error || `Ошибка ${r.status}`);
  return d;
}

function step(n) {
  [1, 2, 3, 4].forEach((i) => $("#s" + i).classList.toggle("hidden", i !== n));
  document.body.dataset.step = String(n);
  $(".steps").style.setProperty("--step", String(n - 1));
  const card = $("#s" + n);
  card.classList.remove("enter");
  void card.offsetWidth;
  card.classList.add("enter");
  card.querySelector(".step-body").scrollTop = 0;
  document.querySelectorAll(".steps li").forEach((li) => { const s = +li.dataset.s; li.className = s === n ? "on" : s < n ? "done" : ""; });
}

$("#ver").textContent = window.VERSION || "";

function selectSpeaker(speaker) {
  selectedSpeaker.set(speaker);
  step(2);
}

const STAGES = [["Подключение", /Подключаюсь|Определена|Найден Джарвис/], ["Место", /Стираю|Удаляю|Свободно на колонке/], ["Файлы", /Копирую|%$/],
  ["Автозапуск", /Выключаю|Добавляю автозапуск/], ["Запуск панели", /Проверяю запуск панели/], ["Готово", /Готово/]];
function drawStages(lines, done, error, warning) {
  let at = 0, pct = null;
  lines.forEach((l) => { STAGES.forEach(([, re], i) => { if (re.test(l) && i >= at) at = i; }); const m = l.match(/(\d+)%$/); if (m) pct = +m[1]; });
  if (done && !error) at = STAGES.length;
  $("#stages").innerHTML = STAGES.map(([name], i) => {
    const manual = warning && name === "Автозапуск";
    const state = manual ? "bad" : i < at ? "done" : i === at ? (error ? "bad" : "on") : "";
    const detail = manual ? " <small>Вручную</small>" : i === at && i === 2 && pct != null && !done ? ` <small>${pct}%</small>` : "";
    return `<li class="${state}"><i aria-hidden="true"></i>${name}${detail}</li>`;
  }).join("");
  const p = $("#install-progress");
  if (done) p.value = 100; else p.value = Math.round(((at + (at === 2 && pct != null ? pct / 100 : 0.4)) / STAGES.length) * 100);
}
function run(title) {
  step(4);
  $("#stages").hidden = !title.startsWith("Устанавливаю");
  drawStages([], false, false);
  $("#s4-title").textContent = title;
  $("#install-progress").removeAttribute("value");
  $("#install-orb").classList.remove("done", "error");
  $("#job-description").textContent = "Переносим файлы и готовим всё для первого разговора. Пока можно познакомиться с возможностями " + (window.matchMedia("(max-width: 760px)").matches ? "ниже." : "справа.");
  $("#job-status").textContent = "Подключаемся к колонке…";
  $("#job-details").open = false;
  $("#open").classList.add("hidden");
  $("#again").classList.add("hidden");
  const t = setInterval(async () => {
    let d;
    try { d = await api("GET", "/api/job"); } catch { return; }
    $("#log").textContent = (d.lines || []).join("\n");
    $("#log").scrollTop = 1e9;
    $("#job-status").textContent = d.error || d.warning || ((d.lines || []).filter((l) => !/^\s*\d+%$/.test(l)).at(-1)) || "Подготавливаем установку…";
    drawStages(d.lines || [], d.done, d.error, d.warning);
    if (d.done) {
      clearInterval(t);
      $("#s4-title").textContent = d.error ? "Нужна ваша помощь." : d.warning ? "Настройте автозапуск." : "Всё готово.";
      $("#install-progress").value = 100;
      $("#install-orb").classList.add(d.error ? "error" : "done");
      $("#job-description").textContent = d.error ? "Установка остановлена. Причина показана ниже; подробности есть в журнале." : d.warning ? "Джарвис установлен и уже работает." : d.url ? "Джарвис установлен. Откройте панель, подключите любимый AI-сервис и выберите голос." : "Операция завершена. Можно вернуться к выбору колонки.";
      if (d.error) $("#job-details").open = true;
      if (d.url) { $("#open").href = d.url; $("#open").classList.remove("hidden"); }
      $("#again").classList.remove("hidden");
    }
  }, 700);
}
$("#again").addEventListener("click", () => { $("#log").textContent = ""; step(1); });
mount(SpeakerDiscovery, { target: $("#s1"), props: { api, onSelect: selectSpeaker } });
mount(DeviceCheck, {
  target: $("#s2"),
  props: {
    api,
    selectedSpeaker,
    onBack: () => step(1),
    onContinue: () => step(3),
    onUninstalled: () => run("Удаляю…"),
  },
});
mount(InstallSettings, {
  target: $("#s3"),
  props: {
    api,
    selectedSpeaker,
    onBack: () => step(2),
    onStarted: () => run("Устанавливаю…"),
  },
});
step(1);

// Same cursor-edge treatment used by the panel, limited to one paint per frame.
let pointerFrame = 0, lastPointer = null;
document.addEventListener("pointermove", event => {
  if (event.pointerType === "touch" || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  lastPointer = event;
  if (pointerFrame) return;
  pointerFrame = requestAnimationFrame(() => {
    pointerFrame = 0;
    const card = lastPointer.target.closest(".card");
    if (!card) return;
    const box = card.getBoundingClientRect();
    card.style.setProperty("--mx", (lastPointer.clientX - box.left) + "px");
    card.style.setProperty("--my", (lastPointer.clientY - box.top) + "px");
  });
});

mount(Showcase, { target: $(".setup-grid") });
