// Плагины: данные и действия (поставить, обновить, включить, настроить, удалить).
// Своей страницы у плагинов больше нет — они живут на странице «Подключения» рядом с сервисами (connections.js).
import { $$, ICONS, act, api, esc, icon, tip, toast, upload } from "./core.js";
import { S } from "./main.js";

export const PL = { data: null, list: null, catErr: "", loading: false };
export const PL_KIND = { lua: "Lua", rust: "Пакет Rust", module: "Встроенный Rust" };
export const plIcon = (n) => icon(ICONS[n] ? n : "puzzle");
export const plSize = (b) => (b < 1048576 ? `${Math.max(1, Math.round(b / 1024))} КБ` : `${(b / 1048576).toFixed(1).replace(".", ",")} МБ`);
export const plBadge = (official) => official ? `<span class="plg-b ok">${icon("shield")}официальный</span>` : `<span class="plg-b warn">неофициальный</span>`;

export async function plLoad() {
  try { PL.data = await api("GET", "/api/plugins"); } catch (e) { toast(e.message, true); }
  return PL.data;
}

export async function plCatalog(fresh) {
  PL.loading = true;
  try { const r = await api("GET", "/api/plugins/catalog" + (fresh ? "?fresh=1" : "")); PL.list = r.items || []; PL.catErr = r.error || ""; }
  catch (e) { PL.catErr = e.message; PL.list = PL.list || []; }
  PL.loading = false;
  return PL.list;
}

export function plInstalled(id) { return (PL.data?.installed || []).find((p) => p.id === id); }
// plCatItem — запись каталога для установленного плагина (официальный — по id, сторонний — по репозиторию)
export function plCatItem(p) {
  return (PL.list || []).find((c) => c.id === p.id && (p.official ? c.official : c.repo === p.source));
}
export function plOfficial(id) { return (PL.list || []).find((c) => c.official && c.id === id); }
export const plRepo = () => PL.data?.repo || S.repo || "newfpv/YaJavis";

// plItems — всё, что можно показать: каталог плюс установленное, чего в каталоге нет (из файла, снятое с каталога).
export function plItems() {
  const inst = PL.data?.installed || [];
  const out = (PL.list || []).map((c) => {
    const p = inst.find((p) => p.id === c.id && (c.official ? p.official : p.source === c.repo));
    return { ...c, name: p?.name || c.name, description: p?.description || c.description, inst: p }; // поставленный знает своё имя лучше каталога
  });
  for (const p of inst) {
    if (out.some((x) => x.inst === p)) continue;
    out.push({ id: p.id, repo: p.source, name: p.name, description: p.description, category: p.category, icon: p.icon, kind: p.kind,
      version: p.version, author: p.author, official: p.official, url: p.homepage, inst: p });
  }
  return out;
}

// plState — коротко для плитки: что с плагином сейчас.
export function plState(x) {
  const p = x.inst;
  if (!p) return { cls: "", text: "не установлен" };
  const c = plCatItem(p);
  if (p.error) return p.error.startsWith("заработает") ? { cls: "warn", text: "ждёт обновления" } : { cls: "bad", text: "ошибка" };
  if (!p.enabled) return { cls: "", text: "выключен" };
  if (c?.version && c.version !== p.version) return { cls: "warn", text: "есть обновление" };
  return p.running ? { cls: "ok", text: "работает" } : { cls: "warn", text: "запускается" };
}

// plWarnHtml — предупреждение перед неофициальным плагином (окно рисует страница).
export function plWarnHtml(name) {
  return `<div class="cn-mh"><span class="char-av cn-av warn">${icon("shield")}</span><div class="char-n"><h2 id="cn-mt">Неофициальный плагин</h2>
      <span class="st warn">${esc(name)}</span></div><button class="ib" type="button" data-close aria-label="Закрыть">${icon("x")}</button></div>
    <p>Этот плагин написал не автор YaJarvis, и никто его не проверял. Он будет работать на вашей колонке и сможет ходить в интернет,
      а нативный плагин — вообще всё, что может программа на колонке.</p>
    <p class="muted">Автор YaJarvis не отвечает за сторонние плагины и их последствия. Ставьте только то, чему доверяете: лучше — с открытым кодом и от известного автора.</p>
    <label class="sw"><input type="checkbox" id="pl-warn-ok"><span class="tr"></span>Больше не показывать</label>
    <div class="row cn-foot"><span class="spacer"></span><button class="btn ghost" type="button" data-close>Отмена</button><button class="btn primary" type="button" id="pl-warn-yes">Понимаю, установить</button></div>`;
}
export const plWarned = () => !!(PL.data?.warned || S.settings.plugins_warned);
export async function plMarkWarned() {
  await api("POST", "/api/plugins/warned").catch(() => {});
  if (PL.data) PL.data.warned = true;
  S.settings.plugins_warned = true;
}

// plField — поле настроек плагина по описанию из plugin.json.
export function plField(p, s) {
  const v = p.values?.[s.key];
  const lab = `${esc(s.label || s.key)}${s.hint ? " " + tip(s.hint) : ""}`;
  const at = `data-k="${esc(s.key)}" data-type="${esc(s.type)}"`;
  if (s.type === "bool") return `<label class="sw"><input type="checkbox" ${at}${v ? " checked" : ""}><span class="tr"></span>${lab}</label>`;
  if (s.type === "select") return `<div class="f"><label>${lab}</label><select ${at}>${(s.options || []).map((o) => `<option${o === v ? " selected" : ""}>${esc(o)}</option>`).join("")}</select></div>`;
  if (s.type === "textarea") return `<div class="f"><label>${lab}</label><textarea rows="4" ${at}>${esc(v ?? "")}</textarea></div>`;
  if (s.type === "password") return `<div class="f"><label>${lab}</label><input type="password" autocomplete="off" ${at} placeholder="${v ? `задан ${esc(v)} — оставьте пустым, чтобы не менять` : "не задан"}"></div>`;
  return `<div class="f"><label>${lab}</label><input type="${s.type === "number" ? "number" : "text"}" ${at} value="${esc(v ?? "")}"></div>`;
}

export function plValues(root) {
  const vals = {};
  $$("[data-k]", root).forEach((el) => {
    const ty = el.dataset.type;
    vals[el.dataset.k] = ty === "bool" ? el.checked : ty === "number" ? (el.value === "" ? null : Number(el.value)) : el.value;
  });
  return vals;
}

export async function plInstall(btn, repo, id, then) {
  await act(btn, async () => {
    if (btn) btn.innerHTML = `<span class="spin"></span>Ставлю…`;
    const r = await api("POST", "/api/plugins/install", { repo, id });
    if (r.restarting && !await waitModuleChange()) return;
    await plLoad();
    toast(`«${r.plugin?.name || id}» установлен`);
    await then?.();
  });
}

export async function plUpdate(btn, id, then) {
  const p = plInstalled(id), c = p && plCatItem(p);
  if (!c) return;
  await act(btn, async () => { const r = await api("POST", "/api/plugins/install", { repo: c.repo, id: c.id }); if (r.restarting && !await waitModuleChange()) return; await plLoad(); await then?.(); }, "Плагин обновлён");
}

export async function plEnable(id, on, then) {
  await act(null, async () => { await api("POST", `/api/plugins/${encodeURIComponent(id)}/enable`, { on }); await plLoad(); await then?.(); }, on ? "Плагин включён" : "Плагин выключен");
}

export async function plSave(btn, id, vals, then) {
  await act(btn, async () => { await api("POST", `/api/plugins/${encodeURIComponent(id)}/settings`, vals); await plLoad(); await then?.(); }, "Сохранено");
}

export async function plRemove(btn, id, then) {
  const p = plInstalled(id);
  const kept = ["home", "vpn", "youtube"].includes(p?.id) ? " Вход в аккаунты и ключи останутся на колонке." : "";
  const linked = p?.kind === "module" ? " Код будет освобождён после перехода на точную сборку без этого модуля. Колонка применит её автоматически, если она есть в релизе и автообновление включено." : "";
  if (!confirm(`Удалить плагин «${p?.name || id}» вместе с его отдельными настройками и данными?${kept}${linked}`)) return;
  await act(btn, async () => { const r = await api("DELETE", `/api/plugins/${encodeURIComponent(id)}`); if (r.restarting && !await waitModuleChange()) return; await plLoad(); await then?.(); }, "Плагин удалён");
}

async function waitModuleChange() {
  toast("Применяю изменения. Панель переподключится сама");
  for (let n = 0; n < 90; n++) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    try {
      const state = await api("GET", "/api/plugins");
      if (!state.composition_pending) { location.reload(); return true; }
    } catch { /* The speaker is restarting. */ }
  }
  throw new Error("Колонка не подтвердила применение изменений");
}

export async function plUpload(file, then) {
  await act(null, async () => { const r = await upload("POST", "/api/plugins/upload", file); await plLoad(); toast(`«${r.plugin?.name}» установлен`); await then?.(r.plugin); });
}
