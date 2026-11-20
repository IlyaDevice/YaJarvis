// Плитки на главной и графики.
import { $, Motion, api, tip } from "./core.js";
import { S, page } from "./main.js";

// ---------------------------------------------------------------- плитки на главной (как в yapanel) и графики
export const fmtN = (v, dec = 0) => (v == null || isNaN(v) ? "—" : Number(v).toLocaleString("ru-RU", { minimumFractionDigits: dec, maximumFractionDigits: dec }).replace("-", "−"));
export const KPI = [
  { id: "temp", label: "Температура" },
  { id: "mem", label: "Память", tip: "Сколько оперативной памяти колонки занято. Джарвис занимает около 25 МБ." },
  { id: "cpu", label: "Процессор" },
  { id: "wifi", label: "Wi-Fi", tip: "Уровень сигнала: от −30 до −60 дБм — отлично, до −70 — нормально, ниже — связь может рваться, и Джарвис будет отвечать с задержкой." },
];
export const kpiHtml = (k) => `<div class="kpi" id="k-${k.id}"><span class="label">${k.label}${k.tip ? " " + tip(k.tip) : ""}</span><div class="value"><b>—</b><span></span></div><div class="kbar"><i></i></div><span class="meta"></span>
  <svg class="spark" viewBox="0 0 200 46" aria-hidden="true"><path class="a"/><path class="l"/></svg></div>`;
export let ST = { pts: [], at: 0 };

export async function statsData() {
  ST.at = Date.now();
  try { ST.pts = (await api("GET", "/api/stats?range=900")).points || []; } catch { return; }
  drawKpis();
}

// число «набегает» к новому значению (при первом показе — от нуля)
export function countTo(el, v, dec = 0) {
  if (v == null) { el.textContent = "—"; delete el.dataset.v; return; }
  const first = el.dataset.v == null, from = first ? 0 : +el.dataset.v;
  if (from === v) { if (first) { el.dataset.v = v; el.textContent = fmtN(v, dec); } return; }
  el.dataset.v = v;
  if (Motion.reduce) { el.textContent = fmtN(v, dec); return; }
  const t0 = performance.now(), d = 800;
  const step = (t) => {
    if (+el.dataset.v !== v) return;
    const k = Math.min(1, (t - t0) / d), e = 1 - Math.pow(1 - k, 3);
    el.textContent = fmtN(from + (v - from) * e, dec);
    if (k < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

export function sparkPaths(vals, w = 100, h = 40) {
  const v = vals.filter((x) => x != null && !isNaN(x));
  if (v.length < 2) return null;
  let lo = Math.min(...v), hi = Math.max(...v);
  if (hi - lo < 1e-6) { hi += 1; lo -= 1; }
  const top = h * 0.3;
  const pts = v.map((y, i) => [(i / (v.length - 1)) * w, top + (1 - (y - lo) / (hi - lo)) * (h - top - 2)]);
  const l = pts.map((p, i) => (i ? "L" : "M") + p[0].toFixed(2) + " " + p[1].toFixed(2)).join("");
  return { l, a: l + `L${w} ${h}L0 ${h}Z` };
}

export function setKpi(id, val, unit, frac, meta, series, dec = 0, level = "") {
  const box = $("#k-" + id);
  if (!box) return;
  countTo($(".value b", box), val, dec);
  $(".value span", box).textContent = val == null ? "" : unit;
  const bar = $(".kbar", box);
  bar.className = "kbar" + (level ? " " + level : "");
  $(".kbar i", box).style.setProperty("width", `${Math.round(Motion.clamp(frac || 0, 0, 1) * 100)}%`);
  $(".meta", box).textContent = meta || "";
  const svg = $(".spark", box), w = Math.round(svg.clientWidth) || 200, h = 46, p = sparkPaths(series, w, h);
  if (!p) return;
  svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
  $("path.l", svg).setAttribute("d", p.l);
  $("path.a", svg).setAttribute("d", p.a);
  if (!svg.dataset.drawn) { svg.dataset.drawn = "1"; drawIn($("path.l", svg), $("path.a", svg), 1300); }
}

export function drawKpis() {
  if (!$("#kpis")) return;
  const sys = S.sys || {}, last = ST.pts[ST.pts.length - 1] || {};
  const used = (sys.mem_total_mb || 0) - (sys.mem_avail_mb || 0);
  setKpi("temp", sys.temp_c ? Math.round(sys.temp_c) : null, "°C", (sys.temp_c || 0) / 85, "процессор", ST.pts.map((p) => p.temp || null), 0, sys.temp_c > 75 ? "bad" : sys.temp_c > 65 ? "warn" : "");
  setKpi("mem", sys.mem_total_mb ? used : null, sys.mem_total_mb ? `из ${sys.mem_total_mb} МБ` : "", used / (sys.mem_total_mb || 1), sys.mem_total_mb ? `свободно ${sys.mem_avail_mb} МБ` : "", ST.pts.map((p) => p.mem));
  const cpu = ST.pts.length > 1 ? Math.round(last.cpu) : null;
  const me = ST.pts.length > 1 ? Math.round(last.me || 0) : null;
  setKpi("cpu", cpu, "%", (cpu || 0) / 100, me != null ? `из них Джарвис — ${me}%` : "все ядра колонки", ST.pts.slice(1).map((p) => p.cpu), 0, cpu > 85 ? "warn" : "");
  const w = sys.wifi_dbm || null, q = w ? Motion.clamp((w + 90) / 60, 0, 1) : 0;
  setKpi("wifi", w, "дБм", q, !w ? "" : w >= -60 ? "сигнал отличный" : w >= -70 ? "сигнал нормальный" : "сигнал слабый — ответы могут задерживаться", ST.pts.map((p) => p.wifi || null), 0, w && w < -70 ? "warn" : "");
}

let resizeTimer;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    if (page === "home") drawKpis();
  }, 150);
});

// край карточки светится там, где указатель
document.addEventListener("pointermove", (e) => {
  const c = e.target.closest?.(".card, .sec, .np, .kpi");
  if (!c) return;
  const r = c.getBoundingClientRect();
  c.style.setProperty("--mx", `${e.clientX - r.left}px`);
  c.style.setProperty("--my", `${e.clientY - r.top}px`);
}, { passive: true });

// плашка активного пункта меню переезжает на пружине
export function navPill() {
  const nav = $("#nav");
  if (!nav) return;
  let pill = $(".nav-pill", nav);
  if (!pill) { pill = document.createElement("span"); pill.className = "nav-pill instant"; nav.prepend(pill); nav.classList.add("has-pill"); }
  const t = $(".nav > a.on, .prof.on", nav);
  if (!t || !t.offsetHeight) { pill.style.setProperty("opacity", "0"); return; }
  pill.style.setProperty("transform", `translateY(${t.offsetTop}px)`);
  pill.style.setProperty("height", `${t.offsetHeight}px`);
  pill.style.setProperty("opacity", "1");
  requestAnimationFrame(() => requestAnimationFrame(() => pill.classList.remove("instant")));
}

// ---------------------------------------------------------------- линия графика «прорисовывается» (длина пути считается в пикселях)
export function drawIn(line, area, ms = 1100) {
  if (Motion.reduce || !line.getTotalLength) return;
  const len = line.getTotalLength();
  line.style.setProperty("transition", "none");
  line.style.setProperty("stroke-dasharray", `${len} ${len}`);
  line.style.setProperty("stroke-dashoffset", String(len));
  if (area) { area.style.setProperty("transition", "none"); area.style.setProperty("opacity", "0"); }
  line.getBoundingClientRect();
  requestAnimationFrame(() => {
    line.style.setProperty("transition", `stroke-dashoffset ${ms}ms cubic-bezier(.22,1,.36,1)`);
    line.style.setProperty("stroke-dashoffset", "0");
    if (area) { area.style.setProperty("transition", `opacity ${ms * 0.7}ms ease ${ms * 0.35}ms`); area.style.setProperty("opacity", "1"); }
  });
  setTimeout(() => { if (line.isConnected) { line.style.removeProperty("stroke-dasharray"); line.style.removeProperty("stroke-dashoffset"); } }, ms + 120);
}
