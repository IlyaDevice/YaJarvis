import { $, api, esc } from "./core.js";

function logLevel(line) {
  if (/ошибк|error|fail|не получилось|panic|не удалось|не открылся/i.test(line)) return "bad";
  if (/warn|предупрежд|повтор|ложное/i.test(line)) return "warn";
  return "";
}

export function parseLogLine(line) {
  const match = /^(\d{4}\/\d\d\/\d\d |\[[\d:]+\] \[)?(\S*\s?\d\d:\d\d:\d\d(?:\.\d+)?\]?)?(.*)$/.exec(line) || [];
  return {
    time: (match[1] || "") + (match[2] || ""),
    message: match[3] ?? line,
    level: logLevel(line),
  };
}

// логи на главной: карточка занимает остаток правой колонки; места меньше ~170 px — её не видно и лог не грузим
export const HL = { at: 0, html: "", on: false };
export function homeLogsSetup() {
  const c = $("#hlogs");
  if (!c) return;
  HL.html = ""; HL.on = false;
  new ResizeObserver(() => {
    const on = c.isConnected && getComputedStyle(c).display !== "none" && c.clientHeight >= 170;
    c.classList.toggle("tight", !on);
    if (on && !HL.on) { HL.on = true; homeLogs(); } else HL.on = on;
  }).observe(c);
}
export async function homeLogs() {
  HL.at = Date.now();
  const box = $("#hlogbox");
  if (!box || !HL.on) return;
  const n = Math.max(10, Math.ceil(box.clientHeight / 19) + 4);
  let d;
  try { d = await api("GET", `/api/logs?src=jarvis&n=${n}`); } catch { return; }
  const html = (d.lines || []).map(logLineShort).join("") || `<p class="empty">${esc(d.error || "Пока пусто")}</p>`;
  if (html === HL.html || !box.isConnected) return;
  HL.html = html;
  box.innerHTML = html;
  box.scrollTop = box.scrollHeight;
}

// Короткая строка для главной: время — только минуты и секунды.
function logLineShort(line) {
  const match = /^\d{4}\/\d\d\/\d\d \d\d:(\d\d:\d\d)(?:\.\d+)? (.*)$/.exec(line);
  const { time, message, level } = match
    ? { time: match[1], message: match[2], level: logLevel(match[2]) }
    : parseLogLine(line);
  return `<div class="ll${level ? " " + level : ""}"><span class="lt">${esc(time)}</span>${esc(message)}</div>`;
}
