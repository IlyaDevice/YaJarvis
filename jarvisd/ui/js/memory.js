// Память Джарвиса: что он знает о хозяине и что помнит из разговоров. Всё можно поправить или удалить.
//   досье — короткие факты о человеке (строки «- …»), правятся по одному;
//   конспект — сжатые недавние разговоры; летопись — по месяцам, старше архива;
//   архив — дословные реплики с поиском; удалённое Джарвис больше не вспомнит.
import { $, $$, act, api, esc, icon, plural, tip, toast } from "./core.js";
import { page, reload } from "./main.js";

const MM = { data: null, turns: [], more: false, q: "", edit: "", busy: false, sel: new Set() };
const MEM_PROV = [["chatgpt", "Подписка ChatGPT"], ["gemini", "Google Gemini — ключ"], ["openai", "OpenAI — ключ, платно"]];
const day = (u) => new Date(u * 1000).toLocaleDateString("ru-RU", { day: "numeric", month: "long", year: "numeric" });
const at = (u) => new Date(u * 1000).toLocaleString("ru-RU", { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" });
const time = (u) => new Date(u * 1000).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });

// facts — строки досье: «- факт» → факт; пустые и заголовки без текста пропускаем.
function facts(profile) {
  return String(profile || "").split("\n").map((l) => l.trim()).filter(Boolean).map((l) => l.replace(/^[-•*]\s*/, ""));
}
const joinFacts = (list) => list.map((f) => f.trim()).filter(Boolean).map((f) => "- " + f).join("\n");

export function viewMemory() {
  $("#view").innerHTML = `<header class="ph ph-row"><div><h1>Память</h1><p class="sub">Что Джарвис помнит о вас — общее для всех ассистентов. Неверное поправьте, лишнее удалите</p></div>
    <div class="actions" id="mm-top"></div></header>
  <div id="mm" class="mm"><div class="card plg-empty"><span class="spin"></span><p class="muted">Загружаю память…</p></div></div>`;
  const view = $("#view");
  view.onclick = mmClick;
  view.onchange = mmChange;
  view.oninput = (e) => {
    if (e.target.id === "mm-q") { MM.q = e.target.value; clearTimeout(mmLoadTurns.t); mmLoadTurns.t = setTimeout(() => mmLoadTurns(false), 300); }
  };
  view.onkeydown = (e) => {
    if (page !== "memory") return;
    if (e.key === "Enter" && e.target.id === "mm-add") { e.preventDefault(); $("#mm-add-btn")?.click(); }
    if (e.key === "Enter" && e.target.matches("[data-fact-input]")) { e.preventDefault(); e.target.closest(".mm-fact").querySelector("[data-fact-save]")?.click(); }
    if (e.key === "Escape" && MM.edit) { MM.edit = ""; mmDraw(); }
  };
  MM.edit = ""; MM.sel.clear();
  mmLoad();
}

async function mmLoad() {
  try { MM.data = await api("GET", "/api/memory"); } catch (e) { $("#mm").innerHTML = `<p class="err">${esc(e.message)}</p>`; return; }
  await mmLoadTurns(false);
}

async function mmLoadTurns(more) {
  const before = more && MM.turns.length ? MM.turns[MM.turns.length - 1].n : 0;
  try {
    const r = await api("GET", `/api/memory/archive?limit=40&before=${before}&q=${encodeURIComponent(MM.q)}`);
    MM.turns = more ? [...MM.turns, ...r.turns] : r.turns;
    MM.more = r.more;
  } catch (e) { toast(e.message, true); }
  if (page === "memory") mmDraw();
}

function stat(n, label, sub) {
  return `<div class="mm-stat"><b>${n}</b><span>${label}</span>${sub ? `<small>${sub}</small>` : ""}</div>`;
}

function mmDraw() {
  const box = $("#mm");
  if (!box || !MM.data) return;
  const m = MM.data.memory || {}, ms = MM.data.settings || {}, off = !!ms.off;
  const list = facts(m.profile), chron = (m.chronicle || []).slice().reverse();
  const n = m.archive || 0;
  $("#mm-top").innerHTML = `<label class="sw mm-on"><input type="checkbox" id="mem-on"${off ? "" : " checked"}><span class="tr"></span>Запоминать разговоры</label>`;
  const keepQ = document.activeElement?.id === "mm-q";
  box.innerHTML = `
    ${off ? `<p class="note mm-off">${icon("moon")}<span>Память выключена: каждый разговор начинается с чистого листа. То, что уже записано, осталось — его можно смотреть и править.</span></p>` : ""}
    <div class="mm-stats">${stat(list.length, plural(list.length, "факт о вас", "факта о вас", "фактов о вас"))}${stat(n, plural(n, "реплика в архиве", "реплики в архиве", "реплик в архиве"), m.archive_since ? `с ${day(m.archive_since)}` : "")}
      ${stat(chron.length, plural(chron.length, "месяц в летописи", "месяца в летописи", "месяцев в летописи"))}${stat(m.turns || 0, "в текущем чате", "")}</div>

    <section class="card mm-sec">
      <div class="mm-h"><div><h2>${icon("read")}Что Джарвис знает о вас</h2><p class="muted">Досье: он видит его в начале каждого разговора. Пополняется само, когда разговор сжимается</p></div></div>
      ${list.length ? `<ul class="mm-facts">${list.map((f, i) => MM.edit === "f" + i
        ? `<li class="mm-fact edit"><input type="text" data-fact-input value="${esc(f)}" aria-label="Факт"><button class="btn sm primary" data-fact-save="${i}">Сохранить</button><button class="btn sm ghost" data-cancel>Отмена</button></li>`
        : `<li class="mm-fact"><span>${esc(f)}</span><button class="ib" data-fact-edit="${i}" aria-label="Изменить">${icon("edit")}</button><button class="ib danger" data-fact-del="${i}" aria-label="Удалить">${icon("trash")}</button></li>`).join("")}</ul>`
        : `<p class="mm-empty">Пока пусто. ${n ? "Заполнится, когда разговор сожмётся, — или добавьте сами." : "Расскажите Джарвису о себе или добавьте сами."}</p>`}
      <div class="mm-add"><input type="text" id="mm-add" placeholder="Добавить: например, «не ем мясо» или «дочь зовут Аня»" maxlength="300" aria-label="Новый факт"><button class="btn" id="mm-add-btn">${icon("plus")}Добавить</button></div>
    </section>

    <section class="card mm-sec">
      <div class="mm-h"><div><h2>${icon("todo")}Конспект недавних разговоров</h2><p class="muted">${m.summary_at ? `Обновлён ${at(m.summary_at)}` : "Кто что сказал и когда — сжато"}</p></div>
        <div class="row">${!off && ms.active && n ? `<button class="btn sm" id="mem-compact" data-tip="Сжать разговор в досье и конспект сейчас, не дожидаясь длинного разговора">Обновить сейчас</button>` : ""}
          ${m.summary && MM.edit !== "sum" ? `<button class="btn sm ghost" data-edit="sum">${icon("edit")}Изменить</button>` : ""}</div></div>
      ${MM.edit === "sum" ? `<textarea id="mm-sum" rows="10">${esc(m.summary || "")}</textarea><div class="row"><span class="spacer"></span><button class="btn ghost" data-cancel>Отмена</button><button class="btn primary" id="mm-sum-save">Сохранить</button></div>`
        : m.summary ? `<div class="mem-sum mm-text">${esc(m.summary)}</div>` : `<p class="mm-empty">Пока пусто: появится после первого длинного разговора.</p>`}
    </section>

    ${chron.length ? `<section class="card mm-sec">
      <div class="mm-h"><div><h2>${icon("moon")}Летопись</h2><p class="muted">Разговоры старше архива, сжатые по месяцам</p></div></div>
      <div class="mm-months">${chron.map((c) => MM.edit === "m" + c.month
        ? `<div class="mm-month"><div class="mm-mh"><b>${esc(c.label)}</b></div><textarea data-month-text rows="8">${esc(c.text)}</textarea>
            <div class="row"><span class="spacer"></span><button class="btn ghost sm" data-cancel>Отмена</button><button class="btn primary sm" data-month-save="${esc(c.month)}">Сохранить</button></div></div>`
        : `<details class="mm-month"><summary><b>${esc(c.label)}</b><small>${c.turns} ${plural(c.turns, "реплика", "реплики", "реплик")}</small>
            <button class="ib mm-push" data-month-edit="${esc(c.month)}" aria-label="Изменить">${icon("edit")}</button><button class="ib danger" data-month-del="${esc(c.month)}" aria-label="Удалить">${icon("trash")}</button></summary>
            <div class="mem-sum mm-text">${esc(c.text)}</div></details>`).join("")}</div>
    </section>` : ""}

    <section class="card mm-sec">
      <div class="mm-h"><div><h2>${icon("voice")}Архив разговоров</h2><p class="muted">Дословно, последние ${5000} реплик. На «как я рассказывал» Джарвис ищет здесь сам</p></div></div>
      <div class="mm-tools"><label class="cx-search">${icon("search")}<input id="mm-q" type="search" placeholder="Найти в разговорах" value="${esc(MM.q)}" autocomplete="off" aria-label="Поиск по архиву"></label>
        ${MM.sel.size ? `<button class="btn danger" id="mm-del-sel">${icon("trash")}Удалить выбранные (${MM.sel.size})</button>` : ""}</div>
      ${MM.turns.length ? turnsHtml() : `<p class="mm-empty">${MM.q ? "Ничего не нашлось." : "Архив пока пуст."}</p>`}
      ${MM.more ? `<div class="row"><button class="btn ghost" id="mm-more">Показать ещё</button></div>` : ""}
    </section>

    ${settingsHtml(ms, off)}

    <section class="card mm-sec mm-danger">
      <div class="mm-h"><div><h2>Начать заново</h2><p class="muted">Новый чат — Джарвис забудет конспект и недавние реплики, досье и архив останутся. Стереть — удалить всё, без возврата</p></div></div>
      <div class="row"><button class="btn" id="cg-newchat"${m.turns || m.summary ? "" : " disabled"}>Начать новый чат</button><span class="spacer"></span>
        <button class="btn ghost danger" id="mem-erase"${n || list.length || m.summary ? "" : " disabled"}>${icon("trash")}Стереть всю память</button></div>
    </section>`;
  if (keepQ) { const q = $("#mm-q"); q.focus(); q.setSelectionRange(q.value.length, q.value.length); }
  if (MM.edit) $("#mm [data-fact-input], #mm-sum, #mm [data-month-text]")?.focus();
}

function turnsHtml() {
  let last = "";
  return `<div class="mm-turns">${MM.turns.map((t) => {
    const d = t.day !== last ? `<div class="mm-day">${esc(t.day)}</div>` : "";
    last = t.day;
    const fact = !t.user && /^\[.*\]$/.test(t.bot || "");
    return `${d}<div class="mm-turn${MM.sel.has(t.n) ? " sel" : ""}${fact ? " fact" : ""}"><label class="mm-check" aria-label="Выбрать"><input type="checkbox" data-turn-sel="${t.n}"${MM.sel.has(t.n) ? " checked" : ""}></label>
      <div class="mm-tt"><small>${time(t.ts)}</small>${t.user ? `<p class="mm-u"><b>Вы</b><span>${esc(t.user)}</span></p>` : ""}${t.bot ? `<p class="mm-b"><b>${fact ? "Событие" : "Джарвис"}</b><span>${esc(fact ? t.bot.slice(1, -1) : t.bot)}</span></p>` : ""}</div>
      <button class="ib danger" data-turn-del="${t.n}" aria-label="Удалить реплику">${icon("trash")}</button></div>`;
  }).join("")}</div>`;
}

function settingsHtml(ms, off) {
  if (off) return "";
  const has = ms.has || {}, prov = ms.provider || "", mprov = has[prov] ? prov : ms.active;
  const pname = (id) => MEM_PROV.find(([x]) => x === id)?.[1] || id;
  const mname = (x) => x || "по очереди: кончился лимит — следующая";
  return `<section class="card mm-sec">
    <div class="mm-h"><div><h2>${icon("tune")}Кто сжимает разговоры</h2><p class="muted">Короткий текстовый запрос раз в несколько десятков реплик: старое — в досье и конспект</p></div></div>
    <div class="row">
      <div class="f half"><label for="mem-prov">Модель ${tip("«Сам выбирает» берёт сначала бесплатное: подписку ChatGPT, потом Gemini, потом платный ключ OpenAI. Если один не ответил — следующий.")}</label>
        <select id="mem-prov"><option value=""${prov ? "" : " selected"}>Сам выбирает</option>${MEM_PROV.map(([id, nm]) => `<option value="${id}"${prov === id ? " selected" : ""}${has[id] ? "" : " disabled"}>${esc(nm)}${has[id] ? "" : " — не подключено"}</option>`).join("")}<option value="none"${prov === "none" ? " selected" : ""}>Не сжимать</option></select></div>
      ${mprov && prov !== "none" ? `<div class="f half"><label for="mem-model">Версия</label><select id="mem-model">${(ms.models?.[mprov] || []).map((x) => `<option value="${esc(x)}"${x === (ms.active_model ?? "") ? " selected" : ""}>${esc(mname(x))}</option>`).join("")}</select></div>` : ""}
    </div>
    ${prov === "none" ? `<p class="note">Не сжимается: Джарвис видит только последние реплики, а старое находит в архиве.</p>`
      : ms.active ? `<p class="note">Сейчас сжимает: <b>${esc(pname(ms.active))}</b> · ${esc(mname(ms.active_model))}.${prov && !has[prov] ? " Выбранное не подключено — пока работает автовыбор." : ""}</p>`
      : `<p class="err">Нужна текстовая модель: подписка ChatGPT, ключ Gemini или ключ OpenAI. Пока её нет, старое не сжимается, но архив пишется. <a class="lnk" href="#connections">Подключения</a></p>`}
  </section>`;
}

async function saveProfile(list, msg) {
  MM.data = await api("PUT", "/api/memory", { profile: joinFacts(list) });
  MM.edit = "";
  mmDraw();
  toast(msg);
}

async function mmClick(e) {
  if (page !== "memory") return;
  const t = e.target;
  const list = facts(MM.data?.memory?.profile);
  const b = (sel) => t.closest(sel);
  let x;
  if (b("[data-cancel]")) { MM.edit = ""; mmDraw(); return; }
  if ((x = b("[data-fact-edit]"))) { MM.edit = "f" + x.dataset.factEdit; mmDraw(); return; }
  if ((x = b("[data-fact-save]"))) {
    const v = x.closest(".mm-fact").querySelector("[data-fact-input]").value.trim();
    const next = list.slice(); if (v) next[+x.dataset.factSave] = v; else next.splice(+x.dataset.factSave, 1);
    return act(x, () => saveProfile(next, "Сохранено"));
  }
  if ((x = b("[data-fact-del]"))) {
    const next = list.slice(); next.splice(+x.dataset.factDel, 1);
    return act(x, () => saveProfile(next, "Факт удалён"));
  }
  if ((x = b("#mm-add-btn"))) {
    const v = $("#mm-add").value.trim();
    if (!v) return toast("Напишите, что Джарвису стоит знать", true);
    return act(x, () => saveProfile([...list, v], "Запомнил"));
  }
  if ((x = b("[data-edit]"))) { MM.edit = x.dataset.edit; mmDraw(); return; }
  if ((x = b("#mm-sum-save"))) return act(x, async () => { MM.data = await api("PUT", "/api/memory", { summary: $("#mm-sum").value }); MM.edit = ""; mmDraw(); }, "Конспект сохранён");
  if ((x = b("[data-month-edit]"))) { e.preventDefault(); MM.edit = "m" + x.dataset.monthEdit; mmDraw(); return; }
  if ((x = b("[data-month-save]"))) {
    const text = $("#mm [data-month-text]").value.trim();
    if (!text) return toast("Пустой месяц — удалите его кнопкой с корзиной", true);
    return act(x, async () => { MM.data = await api("PUT", "/api/memory/month", { month: x.dataset.monthSave, text }); MM.edit = ""; mmDraw(); }, "Летопись сохранена");
  }
  if ((x = b("[data-month-del]"))) {
    e.preventDefault();
    if (!confirm("Удалить этот месяц из летописи? Джарвис забудет, о чём тогда говорили.")) return;
    return act(x, async () => { MM.data = await api("PUT", "/api/memory/month", { month: x.dataset.monthDel, text: "" }); mmDraw(); }, "Месяц удалён");
  }
  if ((x = b("[data-turn-del]"))) return forget([+x.dataset.turnDel], x);
  if ((x = b("#mm-del-sel"))) {
    if (!confirm(`Удалить выбранные реплики (${MM.sel.size})? Джарвис их больше не вспомнит.`)) return;
    return forget([...MM.sel], x);
  }
  if ((x = b("#mm-more"))) return act(x, () => mmLoadTurns(true));
  if ((x = b("#mem-compact"))) return act(x, async () => { const r = await api("POST", "/api/memory/compact"); await mmLoad(); if (!r.turns) toast("Сжимать пока нечего"); else toast("Досье и конспект обновлены"); });
  if ((x = b("#cg-newchat"))) {
    if (!confirm("Начать новый чат? Джарвис забудет конспект и недавние реплики. Досье о вас, летопись и архив останутся.")) return;
    return act(x, async () => { await api("POST", "/api/chatgpt/newchat"); await mmLoad(); }, "Новый чат начат");
  }
  if ((x = b("#mem-erase"))) {
    if (!confirm("Стереть всю память Джарвиса — досье о вас, конспект, летопись и архив разговоров? Это не отменить.")) return;
    return act(x, async () => { await api("POST", "/api/chatgpt/newchat?all=1"); MM.sel.clear(); await mmLoad(); }, "Память стёрта");
  }
}

async function forget(ns, btn) {
  await act(btn, async () => {
    const r = await api("POST", "/api/memory/forget", { n: ns });
    ns.forEach((n) => MM.sel.delete(n));
    MM.turns = MM.turns.filter((t) => !ns.includes(t.n));
    MM.data = await api("GET", "/api/memory");
    mmDraw();
    toast(r.deleted === 1 ? "Реплика удалена" : `Удалено реплик: ${r.deleted}`);
  });
}

async function mmChange(e) {
  if (page !== "memory") return;
  const t = e.target;
  if (t.dataset.turnSel) {
    const n = +t.dataset.turnSel;
    t.checked ? MM.sel.add(n) : MM.sel.delete(n);
    mmDraw();
    return;
  }
  if (!["mem-on", "mem-prov", "mem-model"].includes(t.id)) return;
  const ms = MM.data?.settings || {};
  const next = { off: !!ms.off, provider: ms.provider || "", model: ms.model || "" };
  if (t.id === "mem-on") next.off = !t.checked;
  if (t.id === "mem-prov") { next.provider = t.value; next.model = ""; }
  if (t.id === "mem-model") next.model = t.value;
  try {
    await api("PUT", "/api/settings", { memory: next });
    await reload();
    MM.data = await api("GET", "/api/memory");
    mmDraw();
    toast(t.id === "mem-on" ? (next.off ? "Память выключена — каждый разговор с чистого листа" : "Джарвис снова запоминает разговоры") : "Сохранено");
  } catch (err) { toast(err.message, true); }
}
