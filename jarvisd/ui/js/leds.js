// Подсветка: выбор цвета ассистента.
import { $, $$, api, esc } from "./core.js";
import { S } from "./main.js";

// ---------------------------------------------------------------- подсветка: выбор цвета
export const LED_SWATCHES = ["#ffffff", "#4fd1c5", "#00b4ff", "#3d6bff", "#5a2dff", "#b04dff", "#ff4d8d", "#ff5a36", "#ffb020", "#3ddc84"];
export const LED_STATES = [["listening", "Слушает", "кольцо плавно загорается и дышит"], ["thinking", "Думает", "огонёк бежит по кругу"], ["speaking", "Говорит", "вспыхивает в такт голосу"]];

// строка выбора цвета: живое мини-кольцо + образцы + свой цвет
export function ledPickerHtml(led, prefix) {
  return `<div class="ledrows">${LED_STATES.map(([k, n, hint]) => `
    <div class="ledrow" data-k="${k}">
      <span class="ledring ${k}" data-ring="${prefix}-${k}"></span>
      <div class="ledname"><b>${n}</b><small>${hint}</small></div>
      <div class="swatches">${LED_SWATCHES.map((c) => `<button type="button" class="swatch${c === led[k] ? " on" : ""}" data-c="${c}" data-k="${k}" aria-label="Цвет ${c}"></button>`).join("")}
        <label class="swatch custom${LED_SWATCHES.includes(led[k]) ? "" : " on"}" aria-label="Свой цвет"><input type="color" data-k="${k}" value="${esc(led[k])}"></label></div>
    </div>`).join("")}</div>`;
}

// раскрасить образцы и кольца (цвета — через CSSOM: встроенные style="" запрещены политикой страницы)
export function paintLeds(root, led, prefix) {
  $$(".swatch[data-c]", root).forEach((b) => b.style.setProperty("--c", b.dataset.c));
  LED_STATES.forEach(([k]) => {
    const ring = $(`[data-ring="${prefix}-${k}"]`, root);
    if (ring) { ring.style.setProperty("--c", led[k]); ring.style.setProperty("--b", String(0.35 + 0.65 * (led.brightness / 100))); }
    const custom = $(`.ledrow[data-k="${k}"] .swatch.custom`, root);
    if (custom) custom.style.setProperty("--c", led[k]);
  });
}

// обработчики: onPick(k, color) — выбран цвет; на колонке он сразу загорается на 3 секунды
export function ledPickerBind(root, led, prefix, onPick) {
  paintLeds(root, led, prefix);
  const pick = (k, c) => {
    led[k] = c;
    $$(`.ledrow[data-k="${k}"] .swatch`, root).forEach((s) => s.classList.toggle("on", s.dataset.c === c || (s.classList.contains("custom") && !LED_SWATCHES.includes(c))));
    paintLeds(root, led, prefix);
    onPick(k, c);
    if (S.speaker.leds) api("POST", "/api/leds/preview", { mode: k, color: c, brightness: led.brightness }).catch(() => {});
  };
  root.addEventListener("click", (e) => { const s = e.target.closest(".swatch[data-c]"); if (s) pick(s.dataset.k, s.dataset.c); });
  root.addEventListener("input", (e) => { if (e.target.matches('input[type=color]')) { led[e.target.dataset.k] = e.target.value; paintLeds(root, led, prefix); } });
  root.addEventListener("change", (e) => { if (e.target.matches('input[type=color]')) pick(e.target.dataset.k, e.target.value); });
}
