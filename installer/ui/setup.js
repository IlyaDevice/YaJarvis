"use strict";
const $ = (s) => document.querySelector(s);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
let speaker = null;

async function api(method, url, body) {
  const r = await fetch(url, { method, headers: { "Content-Type": "application/json", "X-Token": window.TOKEN }, body: body ? JSON.stringify(body) : undefined });
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(d.error || `Ошибка ${r.status}`);
  return d;
}

function step(n) {
  [1, 2, 3, 4].forEach((i) => $("#s" + i).classList.toggle("hidden", i !== n));
  document.querySelectorAll(".steps li").forEach((li) => { const s = +li.dataset.s; li.className = s === n ? "on" : s < n ? "done" : ""; });
}

$("#ver").textContent = window.VERSION || "";
const TZ = [["Europe/Moscow", "Москва (UTC+3)"], ["Europe/Minsk", "Минск (UTC+3)"], ["Europe/Kaliningrad", "Калининград (UTC+2)"], ["Europe/Samara", "Самара (UTC+4)"],
  ["Asia/Yekaterinburg", "Екатеринбург (UTC+5)"], ["Asia/Almaty", "Алматы (UTC+5)"], ["Asia/Omsk", "Омск (UTC+6)"], ["Asia/Novosibirsk", "Новосибирск (UTC+7)"],
  ["Asia/Krasnoyarsk", "Красноярск (UTC+7)"], ["Asia/Irkutsk", "Иркутск (UTC+8)"], ["Asia/Yakutsk", "Якутск (UTC+9)"], ["Asia/Vladivostok", "Владивосток (UTC+10)"],
  ["Europe/Kyiv", "Киев"], ["Europe/Berlin", "Берлин"], ["UTC", "UTC"]];
TZ.sort((a, b) => (a[0] === "UTC" ? -1 : b[0] === "UTC" ? 1 : 0));
$("#tz").innerHTML = TZ.map(([v, n]) => `<option value="${v}">${n}</option>`).join("");

function speakerRow(sp) {
  const ok = !sp.problems?.length;
  const badge = !ok ? `<span class="badge bad">не подходит</span>` : sp.installed ? `<span class="badge up">Джарвис ${esc(sp.installed)}</span>` : `<span class="badge ok">можно ставить</span>`;
  return `<div class="sp" data-ip="${esc(sp.ip)}"><div class="b"><b>${esc(sp.model || "Колонка")}</b><small>${esc(sp.ip)}${sp.arch ? " · " + esc(sp.arch) : ""}</small></div>${badge}</div>`;
}

$("#scan").addEventListener("click", async (e) => {
  e.target.disabled = true;
  $("#scan-st").innerHTML = `<span class="spin"></span> ищу колонки с ADB…`;
  $("#found").innerHTML = "";
  try {
    const d = await api("GET", "/api/scan");
    const list = d.speakers || [];
    $("#scan-st").textContent = list.length ? `Найдено: ${list.length}` : "Ничего не нашлось. Проверьте, что колонка разлочена и в той же сети, или введите IP вручную.";
    $("#found").innerHTML = list.map(speakerRow).join("");
    window._list = list;
  } catch (err) { $("#scan-st").textContent = err.message; }
  e.target.disabled = false;
});
$("#found").addEventListener("click", (e) => { const row = e.target.closest(".sp"); if (row) show(window._list.find((s) => s.ip === row.dataset.ip)); });
$("#ip-go").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try { show(await api("POST", "/api/check", { ip: $("#ip").value.trim() })); } catch (err) { alert(err.message); }
  e.target.disabled = false;
});

function show(sp) {
  speaker = sp;
  $("#sp-title").textContent = sp.model || "Колонка";
  $("#sp-info").innerHTML = [["IP", sp.ip], ["Процессор", sp.arch || "?"], ["Root", sp.root ? "есть" : "нет"], ["Память", sp.ram_mb ? sp.ram_mb + " МБ" : "?"], ["Свободно места", sp.free_mb ? sp.free_mb + " МБ" : "?"], ["Джарвис", sp.installed || "не установлен"]]
    .map(([k, v]) => `<span>${k}</span><span>${esc(v)}</span>`).join("");
  $("#sp-problems").innerHTML = (sp.problems || []).map((p) => `<div class="problem">${esc(p)}</div>`).join("") + (sp.notes || []).map((n) => `<div class="note">${esc(n)}</div>`).join("");
  const ok = !sp.problems?.length;
  $("#to3").disabled = !ok;
  $("#to3").textContent = sp.installed ? "Обновить" : "Дальше";
  $("#uninstall").classList.toggle("hidden", !sp.installed);
  step(2);
}

document.querySelectorAll("[data-back]").forEach((b) => b.addEventListener("click", () => step(+b.dataset.back)));
$("#to3").addEventListener("click", () => {
  // при обновлении пароль и ключи уже на колонке — поля можно оставить пустыми
  $("#pw").placeholder = speaker.installed ? "оставьте пустым, чтобы не менять" : "минимум 6 символов";
  step(3);
});

$("#install").addEventListener("click", async () => {
  const pw = $("#pw").value;
  $("#s3-err").textContent = "";
  if (!speaker.installed && pw.length < 6) { $("#s3-err").textContent = "Придумайте пароль — минимум 6 символов"; return; }
  if (pw && pw !== $("#pw2").value) { $("#s3-err").textContent = "Пароли не совпадают"; return; }
  const v = $("#vless").value.trim();
  if (v && !v.startsWith("vless://")) { $("#s3-err").textContent = "Ссылка должна начинаться с vless://"; return; }
  try {
    await api("POST", "/api/install", { ip: speaker.ip, password: pw, openai: $("#openai").value.trim(), vless: v, timezone: $("#tz").value });
    $("#pw").value = $("#pw2").value = $("#openai").value = "";
    run("Устанавливаю…");
  } catch (err) { $("#s3-err").textContent = err.message; }
});

$("#uninstall").addEventListener("click", async () => {
  if (!confirm("Удалить Джарвиса с колонки? Она перезагрузится, Алиса останется как была.")) return;
  try { await api("POST", "/api/uninstall", { ip: speaker.ip }); run("Удаляю…"); } catch (err) { alert(err.message); }
});

function run(title) {
  step(4);
  $("#s4-title").innerHTML = `<span class="spin"></span> ${esc(title)}`;
  $("#open").classList.add("hidden");
  $("#again").classList.add("hidden");
  const t = setInterval(async () => {
    let d;
    try { d = await api("GET", "/api/job"); } catch { return; }
    $("#log").textContent = (d.lines || []).join("\n");
    $("#log").scrollTop = 1e9;
    if (d.done) {
      clearInterval(t);
      $("#s4-title").textContent = d.error ? "Не получилось" : "Готово";
      if (d.url) { $("#open").href = d.url; $("#open").classList.remove("hidden"); }
      $("#again").classList.remove("hidden");
    }
  }, 700);
}
$("#again").addEventListener("click", () => { $("#log").textContent = ""; step(1); });
step(1);
