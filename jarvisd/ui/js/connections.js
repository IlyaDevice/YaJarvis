// Подключения: две вкладки на одной странице.
// «Сервисы» — ключи и входы в аккаунты, плитками по разделам; всё остальное — в окне по нажатию.
// «Плагины» — что стоит на колонке (список: включить, настроить) и что можно поставить.
// Сервису, который живёт в плагине (YouTube, умный дом, VPN), окно предлагает поставить плагин и показывает его.
import { $, $$, act, api, esc, icon, plural, tip, toast } from "./core.js";
import { S, page, reload } from "./main.js";
import { PL, PL_KIND, plBadge, plCatItem, plCatalog, plEnable, plField, plIcon, plInstall, plInstalled, plItems, plLoad, plMarkWarned,
  plOfficial, plRemove, plRepo, plSave, plSize, plState, plUpdate, plUpload, plValues, plWarnHtml, plWarned } from "./plugins.js";

// ---------------------------------------------------------------- характеры
// ---------------------------------------------------------------- подключения
// Подключения — плитками, как «Характеры»: на плитке назначение и состояние, всё остальное — в окне по клику.
// Новое подключение = ещё одна запись в connDefs().
export const CONN = { open: "", net: {}, haEntities: [], tab: "svc" };
const servicePlugin = (id) => S.providers?.[id]?.plugin || "home";

export function haRoomRow(r = {}) {
  const options = (filter, value) => `<option value="">Выберите…</option>` +
    [...new Map([...(value ? [{entity_id:value,attributes:{friendly_name:value}}] : []), ...CONN.haEntities.filter(filter)].map(e => [e.entity_id,e])).values()]
      .map(e => `<option value="${esc(e.entity_id)}"${e.entity_id===value?" selected":""}>${esc(e.attributes?.friendly_name || e.entity_id)}</option>`).join("");
  return `<div class="card" data-ha-room data-ha-binding="${esc(JSON.stringify(r))}"><div class="f"><label>Название комнаты</label><input class="ha-name" value="${esc(r.name || "")}" placeholder="Кухня"></div>
    <div class="f"><label>Пылесос</label><select class="ha-vacuum">${options(e=>e.entity_id.startsWith("vacuum."),r.vacuum)}</select></div>
    <div class="f"><label>Сценарий или действие уборки этой комнаты</label><select class="ha-entity">${options(e=>/^(script|notify|text)\./.test(e.entity_id),r.entity)}</select></div>
    <div class="f"><label>Номера зон на карте — только для действия Xiaomi</label><input class="ha-ids" value="${esc(r.room_ids || "")}" placeholder="Например, 3 или 3,9"></div>
    <button class="btn ghost" data-ha-remove>Убрать комнату</button></div>`;
}
export const NET_NAMES = { openai: "OpenAI", gemini: "Gemini", grok: "Grok", elevenlabs: "ElevenLabs", youtube: "YouTube" };
export const VPN_NAMES = [["chatgpt", "Подписка ChatGPT"], ["openai", "OpenAI"], ["gemini", "Google Gemini"], ["grok", "xAI Grok"],
  ["elevenlabs", "ElevenLabs"], ["youtube", "YouTube"], ["smartlife", "Smart Life"], ["xiaomi", "Xiaomi Home"], ["gdrive", "Google Диск"], ["yandex", "Яндекс"], ["plugins", "Плагины"]];
export function smartLifeConn() {
  const t = S.smartlife || {}, lg = t.login || {}, devs = t.devices || [];
  const local = devs.filter((d) => d.local).length;
  const list = devs.length ? `<div class="xm-devs">${devs.map((d) => `<div class="xm-dev sl-dev${d.online ? "" : " off"}"><span class="sl-way ${d.local ? "lan" : "cloud"}" data-tip="${d.local ? "По домашней сети — мгновенно" : "Через облако Tuya — около полсекунды. Устройства Zigbee (через шлюз) всегда идут так"}">${icon(d.local ? "wifi" : "cloud")}</span><b>${esc(d.name)}</b><small>${esc([d.room, d.product].filter(Boolean).join(" · "))}${d.online ? "" : " · не в сети"}</small></div>`).join("")}</div>
      <p class="note">${local ? `По домашней сети, мгновенно: ${local} из ${devs.length}. Остальные — через облако Tuya.` : "Устройства в домашней сети ещё не откликнулись — пока команды идут через облако Tuya. Обычно они находятся за минуту."}</p>`
    : `<p class="note">Устройств не нашлось. Если они есть в Smart Life — нажмите «Обновить устройства».</p>`;
  return { id: "smartlife", group: "home", plugin: servicePlugin("smartlife"), mono: "SL", name: "Smart Life", about: "Лампы, розетки и другие устройства Tuya — напрямую, без Алисы",
    more: "«Включи/выключи …» выполняется сразу по домашней сети, за доли секунды",
    on: !!t.signed_in, wait: !t.signed_in && !!lg.code, state: t.signed_in ? `${devs.length} ${plural(devs.length, "устройство", "устройства", "устройств")}` : lg.code ? "ждёт QR-код" : "не подключено",
    body: t.signed_in ? `<div class="cn-ok">${icon("done")}<span>Колонка вошла в Smart Life</span></div>${list}
        <div class="row"><button class="btn" id="sl-refresh">Обновить устройства</button></div>${vpnRow("smartlife")}`
      : lg.code ? `<div class="xm-qr"><img src="/api/smartlife/qr?t=${lg.at || 0}" alt="QR-код для входа в Smart Life" width="220" height="220">
          <ol class="how cn-steps"><li>Откройте Smart Life → значок сканера в правом верхнем углу.</li><li>Отсканируйте код и подтвердите вход.</li><li>Это окно само увидит вход и найдёт устройства.</li></ol></div>`
      : `${lg.error ? `<p class="err">${esc(lg.error)}</p>` : ""}
        <div class="f"><label for="sl-code">Код пользователя ${tip("В Smart Life: «Я» → шестерёнка «Настройки» → «Аккаунт и безопасность» → «Код пользователя». Это не пароль.")}</label>
          <div class="row"><input type="text" id="sl-code" placeholder="Например, AbC123x" autocomplete="off" spellcheck="false"><button class="btn primary" id="sl-in">Получить QR-код</button></div></div>
        <p class="note">Код пользователя — в Smart Life: «Я» → «Настройки» → «Аккаунт и безопасность». Потом отсканируете QR в самом приложении. Пароль колонка не видит.</p>${vpnRow("smartlife")}`,
    drop: t.signed_in ? `<button class="btn ghost danger" id="sl-out">Выйти</button>` : "" };
}
export function xiaomiConn() {
  const x = S.xiaomi || {}, lg = x.login || {}, devs = x.devices || [];
  const n = devs.length, word = n % 10 === 1 && n % 100 !== 11 ? "устройство" : [2, 3, 4].includes(n % 10) && ![12, 13, 14].includes(n % 100) ? "устройства" : "устройств";
  const list = devs.length ? `<div class="xm-devs">${devs.map((d) => `<div class="xm-dev${d.online ? "" : " off"}"><b>${esc(d.name)}</b><small>${esc([d.room, d.type || d.model, d.region && "сервер " + d.region].filter(Boolean).join(" · "))}${d.online ? "" : " · не в сети"}</small></div>`).join("")}</div>`
    : `<p class="note">Устройств не нашлось. Если они есть в Mi Home — нажмите «Обновить устройства».</p>`;
  return { id: "xiaomi", group: "home", plugin: servicePlugin("xiaomi"), mono: "Mi", name: "Xiaomi Home", about: "Лампы, розетки, пылесосы и другие устройства из приложения Mi Home",
    more: "Джарвис управляет ими голосом: «включи лампу в спальне», «запусти пылесос»",
    on: !!x.signed_in, wait: !x.signed_in && !!lg.code, state: x.signed_in ? `${n} ${word}` : lg.code ? "ждёт QR-код" : "не подключено",
    body: x.signed_in ? `<div class="cn-ok">${icon("done")}<span>Колонка вошла в Xiaomi Home${x.region ? `<small>${x.region.includes(",") ? "серверы" : "сервер"} ${esc(x.region)}</small>` : ""}</span></div>${list}
        <div class="row"><button class="btn" id="xm-refresh">Обновить устройства</button></div>${vpnRow("xiaomi")}`
      : lg.code ? `<div class="xm-qr"><img src="/api/xiaomi/qr?t=${lg.at || 0}" alt="QR-код для входа в Xiaomi" width="220" height="220">
          <ol class="how cn-steps"><li>Отсканируйте код камерой телефона или в приложении Mi Home: <b>+</b> → <b>Сканировать</b>.</li>
          <li>Войдите в свой аккаунт Xiaomi и подтвердите вход.</li><li>Это окно само увидит вход и найдёт устройства.</li></ol></div>
          ${lg.url ? `<p class="note">Открыли эту страницу на телефоне? <a href="${esc(lg.url)}" target="_blank" rel="noopener">Войти по ссылке</a></p>` : ""}`
      : `${lg.error ? `<p class="err">${esc(lg.error)}</p>` : ""}<div class="row"><button class="btn primary" id="xm-in">Войти по QR-коду</button></div>
          <p class="note">Пароль колонка не видит: вы подтверждаете вход на телефоне. Устройства берутся из того же аккаунта, что и в Mi Home.</p>${vpnRow("xiaomi")}`,
    drop: x.signed_in ? `<button class="btn ghost danger" id="xm-out">Выйти</button>` : "" };
}
// галочка «через VPN» у подключения
export function vpnRow(svc) {
  const on = !!S.vpn?.[svc], set = !!S.vless?.set;
  return `<div class="cn-vpn"><label class="sw"><input type="checkbox" data-vpn="${svc}"${on ? " checked" : ""}><span class="tr"></span>Через VPN</label>
    <small class="muted">${!set ? "VPN не задан — галочка заработает, когда добавите его в «Сеть → VPN»" : on ? "идёт через ваш VPN" : "напрямую, без VPN"}</small></div>`;
}
export function geminiKeysHtml() {
  const list = S.gemini_keys || [];
  if (list.length < 2) return list.length && list[0].spent ? `<p class="note">Лимит кончился у ${list[0].spent} из ${list[0].models} моделей этого ключа. Добавьте ещё ключ — Джарвис перейдёт на него сам.</p>` : "";
  return `<div class="ek-list">${list.map((k, i) => `<div class="ek${!k.spent && !list.slice(0, i).some((x) => x.spent < x.models) ? " on" : ""}${k.spent >= k.models ? " rest" : ""}"><b>${i + 1}</b><code>${esc(k.tail)}</code>
      <span class="ek-st">${k.spent >= k.models ? "лимит кончился · ждёт" : k.spent ? `лимит кончился у ${k.spent} из ${k.models} моделей` : "лимит есть"}</span><span class="spacer"></span>
      <button class="ib" data-gk-remove="${i}" aria-label="Убрать ключ ${i + 1}">${icon("x")}</button></div>`).join("")}</div>
    <p class="note">У каждого ключа свой бесплатный лимит. Кончился у всех моделей первого — Джарвис сам переходит на следующий.</p>`;
}
export function elevenKeysHtml() {
  const list = S.eleven_keys || [];
  if (!list.length) return "";
  const date = (u) => new Date(u * 1000).toLocaleDateString("ru-RU", { day: "numeric", month: "long" });
  return `<div class="ek-list">${list.map((k, i) => `<div class="ek${k.active ? " on" : ""}${k.until ? " rest" : ""}"><b>${i + 1}</b><code>${esc(k.tail)}</code>
      <span class="ek-st">${k.until ? `квота кончилась · ждёт до ${date(k.until)}` : k.active ? "сейчас работает" : "в запасе"}</span><span class="spacer"></span>
      ${k.until ? `<button class="btn sm ghost" data-ek-reset="${i}" data-tip="Например, если докупили символы">Вернуть</button>` : ""}
      <button class="ib" data-ek-remove="${i}" aria-label="Убрать ключ ${i + 1}">${icon("x")}</button></div>`).join("")}</div>
    <p class="note">Работает первый ключ с квотой. Кончилась — ключ откладывается до сброса лимита (обычно через месяц), и Джарвис сам берёт следующий.</p>`;
}

export function connDefs() {
  const k = S.keys, v = S.vless, cg = S.chatgpt, lg = cg.login || {}, yt = S.youtube || {}, ya = yt.account || {};
  const keyBody = (id, place, how, label) => `<div class="f"><label for="k-${id}">${label ? label[0] : k[id] ? "Новый ключ" : "Ключ"}</label><div class="row"><input type="password" id="k-${id}" placeholder="${place}" autocomplete="off"><button class="btn primary" data-key="${id}">${label ? label[1] : k[id] ? "Заменить" : "Сохранить"}</button></div></div>
    ${how ? `<details${k[id] ? "" : " open"}><summary>Где его взять</summary><ol class="how">${how}</ol></details>` : ""}`;
  const key = (id, o) => ({ id, group: o.group, plugin: o.plugin || id, mono: o.mono, name: o.name, about: o.about, more: o.more,
    on: !!k[id], need: !k[id] && !o.optional, state: k[id] ? `ключ ${esc(String(k[id]).replace(/^задан,?\s*/, ""))}` : o.optional ? "не подключено" : "нужен ключ",
    body: (o.top || "") + keyBody(id, o.place, o.how, o.label) + (o.vpn ? vpnRow(o.vpn) : ""), drop: k[id] ? `<button class="btn ghost danger" data-clear="${id}">${o.dropText || "Убрать ключ"}</button>` : "" });
  return [
    { id:"homeassistant", group:"home", plugin:servicePlugin("homeassistant"), mono:"HA", name:"Home Assistant", about:"Устройства дома, Xiaomi Home и уборка выбранных комнат",
      on:!!S.home_assistant?.configured, state:S.home_assistant?.configured?"подключено":"не подключено",
      body:`<p class="note">Подключите свой сервер Home Assistant. Аккаунт Xiaomi, регион и устройства выбираются в самом Home Assistant.</p>
        <div class="f"><label for="ha-url">Адрес сервера</label><input id="ha-url" type="url" value="${esc(S.home_assistant?.url || "")}" placeholder="http://192.168.1.10:8123"></div>
        <div class="f"><label for="ha-token">Долгосрочный токен из профиля Home Assistant</label><input id="ha-token" type="password" autocomplete="off" placeholder="${S.home_assistant?.configured?"Оставьте пустым, чтобы сохранить текущий":"Вставьте токен"}"></div>
        <div class="row"><button class="btn primary" id="ha-save">Проверить и сохранить</button>${S.home_assistant?.configured?`<button class="btn" id="ha-load">Загрузить устройства</button><a href="${esc(S.home_assistant.url)}/config/integrations/dashboard" target="_blank" rel="noopener">Открыть Home Assistant</a>`:""}</div>
        <p class="note">Для Xiaomi добавьте интеграцию Xiaomi Home и войдите в свой аккаунт. Название комнаты в приложении и номер зоны на карте пылесоса могут различаться.</p>
        <h3>Уборка по комнатам</h3><p class="note">Выберите готовый сценарий уборки либо действие Xiaomi с номерами зон. Без соответствия Джарвис попросит настроить комнату.</p>
        <div id="ha-rooms">${(S.home_assistant?.rooms || []).map(haRoomRow).join("")}</div><div class="row"><button class="btn" id="ha-discover-rooms">Найти комнаты пылесоса</button><button class="btn" id="ha-add-room">Добавить комнату</button></div>`,
      drop:S.home_assistant?.configured?`<button class="btn ghost danger" id="ha-disconnect">Отключить</button>`:"" },
    { id: "chatgpt", plugin: "chatgpt", group: "voice", mono: "GP", name: "Подписка ChatGPT", about: "Голос Джарвиса за счёт подписки Plus или Pro — без оплаты API за минуты",
      more: "Включается у ассистента: движок «GPT-Live по подписке».",
      on: !!cg.signed_in, state: cg.signed_in ? esc(["вход выполнен", cg.plan].filter(Boolean).join(" · ")) : lg.code ? "ждёт код" : "не подключено",
      wait: !cg.signed_in && !!lg.code,
      body: cg.signed_in ? `<div class="cn-ok">${icon("done")}<span>Колонка вошла в ChatGPT${cg.plan ? ` · <b>${esc(cg.plan)}</b>` : ""}${cg.email ? `<small>${esc(cg.email)}</small>` : ""}</span></div>
          ${cg.limited_until * 1000 > Date.now() ? `<p class="err">Кончился лимит голоса в подписке. До ${new Date(cg.limited_until * 1000).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })} Джарвис говорит через Gemini${S.keys?.gemini ? "" : " — но ключа Gemini нет, добавьте его ниже"}.</p>` : ""}
          <p class="note">Все звонки — один разговор с общей памятью. Что Джарвис помнит, видно и правится на странице <a class="lnk" href="#memory">«Память»</a>.</p>`
        : lg.code ? `<ol class="how cn-steps"><li>Откройте <a href="${esc(lg.url)}" target="_blank" rel="noopener">${esc(lg.url.replace("https://", ""))}</a> (из России и Беларуси — через VPN) и войдите в свой ChatGPT.</li><li>Введите код: <b class="code-big">${esc(lg.code)}</b></li><li>Это окно само увидит вход — ничего нажимать не нужно.</li></ol>`
        : `${lg.error ? `<p class="err">${esc(lg.error)}</p>` : ""}<div class="row"><button class="btn primary" id="cg-in">Войти в ChatGPT</button></div>
          <p class="note">Колонка получит свой вход, пароль она не видит. Способ неофициальный, как у Codex: OpenAI может его изменить.</p>`
        + vpnRow("chatgpt"),
      drop: cg.signed_in ? `<button class="btn ghost danger" id="cg-out">Выйти</button>` : "" },
    key("openai", { plugin: "openai", vpn: "openai", group: "voice", mono: "AI", name: "OpenAI", place: "sk-…", about: "Голос и «мозг» Джарвиса. Живой разговор — примерно 5 центов в минуту",
      how: "<li>Откройте platform.openai.com и войдите.</li><li>Слева — API keys → Create new secret key.</li><li>Скопируйте ключ (начинается с sk-) и вставьте сюда.</li><li>Пополните баланс в разделе Billing — от 5 $.</li>" }),
    key("gemini", { plugin: "gemini", vpn: "gemini", top: geminiKeysHtml(), label: k.gemini ? ["Ещё ключ — в запас", "Добавить"] : null,
      dropText: (S.gemini_keys || []).length > 1 ? "Убрать все ключи" : "Убрать ключ", group: "voice", mono: "Ge", name: "Google Gemini", place: "AIza…", optional: true, about: "Gemini Live — примерно вдвое дешевле GPT-Live, есть бесплатный уровень. Можно добавить несколько ключей: у каждого свой бесплатный лимит",
      more: "Какой движок отвечает, выбирается у ассистента.",
      how: "<li>Откройте aistudio.google.com и войдите в аккаунт Google (из России и Беларуси — через VPN).</li><li>Нажмите Get API key → Create API key.</li><li>Скопируйте ключ и вставьте сюда.</li>" }),
    key("grok", { plugin: "grok", vpn: "grok", group: "voice", mono: "xA", name: "xAI Grok", place: "xai-…", optional: true, about: "Grok Voice — сам ищет в интернете",
      more: "Какой движок отвечает, выбирается у ассистента.",
      how: "<li>Откройте console.x.ai и войдите.</li><li>Пополните баланс (Billing → Credits) — без кредитов ключ не работает.</li><li>API Keys → Create API key, скопируйте ключ (начинается с xai-) и вставьте сюда.</li>" }),
    key("elevenlabs", { plugin: "elevenlabs", vpn: "elevenlabs", top: elevenKeysHtml(), label: k.elevenlabs ? ["Ещё ключ — в запас", "Добавить"] : null,
      dropText: (S.eleven_keys || []).length > 1 ? "Убрать все ключи" : "Убрать ключ", group: "voice", mono: "11", name: "ElevenLabs", place: "sk_…", optional: true, about: "Распознавание речи и голоса классического режима. Выбирается в настройках ассистента → Мозг" }),
    key("yandex", { vpn: "yandex", group: "home", plugin: servicePlugin("yandex"), mono: "Я", name: "Яндекс Умный дом", place: "y0_…", about: "Джарвис сам управляет светом, пылесосом и сценариями",
      more: "В режиме «через Алису» можно обойтись без него.",
      how: "<li>Нужен OAuth-токен Яндекса с правами iot:view и iot:control.</li><li>Его выдаёт приложение, зарегистрированное на oauth.yandex.ru (тип «Веб-сервисы», права «Умный дом»).</li><li>Вставьте токен (начинается с y0_).</li>" }),
    smartLifeConn(),
    xiaomiConn(),
    { id: "youtube", group: "media", plugin: "youtube", mono: "YT", name: "YouTube", about: "Музыка и видео голосом. Вход — для ваших рекомендаций и видео 18+", more: "Там, где YouTube заблокирован (Россия), включите «Через VPN»",
      on: !!(ya.signed_in || yt.set), state: ya.signed_in ? "вход выполнен" : yt.set ? "куки загружены" : ya.code ? "ждёт код" : "не подключено",
      wait: !ya.signed_in && !!ya.code,
      body: ya.code ? `<div class="ycode"><span>Откройте <a href="${esc(ya.url)}" target="_blank" rel="noopener">${esc(ya.url.replace("https://", ""))}</a> и введите</span><b class="code-big" role="button" tabindex="0" title="Нажмите, чтобы скопировать">${esc(ya.code)}</b><small class="muted">окно само увидит вход</small></div>`
        : ya.signed_in ? `<div class="cn-ok">${icon("done")}<span>Колонка вошла в YouTube<small>видео 18+ тоже играют</small></span></div>`
        : `${ya.error ? `<p class="err">${esc(ya.error)}</p>` : ""}<div class="row"><button class="btn primary" id="ya-in">Войти в YouTube</button><small class="muted">кодом, как на телевизоре — пароль колонка не видит</small></div>
          <details class="mini"><summary>Не получается кодом? Куки браузера</summary><ol class="how"><li>Расширение «Get cookies.txt LOCALLY» → окно инкогнито → войдите на youtube.com.</li><li>Export (Netscape) → скопируйте весь текст сюда → закройте окно, не выходя из аккаунта.</li></ol>
          <textarea id="yt-cookies" class="code" placeholder="# Netscape HTTP Cookie File…" spellcheck="false" aria-label="Куки YouTube"></textarea>
          <div class="row"><button class="btn sm" id="yt-save">${yt.set ? "Заменить" : "Сохранить и проверить"}</button></div></details>`
        + vpnRow("youtube"),
      drop: ya.signed_in ? `<button class="btn ghost danger" id="ya-out">Выйти</button>` : yt.set ? `<button class="btn ghost danger" id="yt-clear">Убрать куки</button>` : "" },
    { id: "vpn", group: "system", plugin: "vpn", icon: "shield", name: "VPN для Джарвиса", about: "Через VPN идёт только сам Джарвис, Алиса — всегда напрямую. Что именно пускать через VPN — галочки ниже",
      on: !!v.set && !v.error, bad: !!v.error, need: !v.set, state: v.set ? (v.error ? "ошибка" : esc(v.name || v.host)) : "не задан",
      body: `${v.set && v.host ? `<div class="vpn ${v.error ? "bad" : "ok"}"><b>${esc(v.name || v.host)}</b><small>${esc(v.host)}:${esc(v.port)} · ${esc(v.network)} + ${esc(v.security)}</small></div>` : v.set ? `<p class="err">${esc(v.error || "VPN запускается…")}</p>` : `<p class="note">Пока не задан — колонка ходит в интернет напрямую. Вставьте ссылку vless://… из вашего VPN (3x-ui, Marzban, Hiddify и т. п.).</p>`}
        ${v.error ? `<p class="err">${esc(v.error)}</p>` : ""}
        <textarea id="vless" class="code" placeholder="vless://…" spellcheck="false" aria-label="Ссылка VLESS"></textarea>
        <div class="row"><button class="btn primary" id="vless-save">${v.set ? "Заменить ссылку" : "Сохранить ссылку"}</button><span class="spacer"></span><button class="btn" data-net>Проверить связь</button></div>
        <div class="netres" id="net-res"></div>
        <h3>Что идёт через VPN</h3><div class="vpn-list">${VPN_NAMES.map(([id, n]) => `<label class="sw"><input type="checkbox" data-vpn="${id}"${S.vpn?.[id] ? " checked" : ""}><span class="tr"></span>${n}</label>`).join("")}</div>`,
      drop: v.set ? `<button class="btn ghost danger" id="vless-clear">Убрать VPN</button>` : "" },
  ];
}

// Разделы вкладки «Сервисы».
export const CONN_GROUPS = [
  ["voice", "Голос и мозг", "Чем Джарвис говорит и думает. Достаточно одного"],
  ["home", "Умный дом", "Свет, розетки, пылесосы и сценарии"],
  ["media", "Музыка и видео", ""],
  ["system", "Сеть", ""],
];
// плагины, на которых держатся сервисы
const HOSTED = { home: "Умный дом", youtube: "YouTube", vpn: "VPN" };
const plKey = (x) => "pl:" + (x.official ? x.id : (x.repo || "file") + "/" + x.id);

// items — всё, что на странице: сервисы ({kind:"svc"}) и плагины ({kind:"pl"}).
function connItems() {
  // сервис из плагина виден, только когда плагин стоит и включён: оболочку человек собирает сам
  const svc = connDefs().filter((c) => !c.plugin || plInstalled(c.plugin)?.enabled).map((c) => ({ ...c, kind: "svc", key: "svc:" + c.id }));
  const pls = plItems().map((x) => ({ kind: "pl", key: plKey(x), x, id: x.id, name: x.name, about: esc(x.description || ""), on: !!x.inst, icon: x.inst?.icon || x.icon }));
  return [...svc, ...pls];
}

export function viewConnections() {
  $("#view").innerHTML = `<header class="ph ph-row"><div><h1>Подключения</h1><p class="sub">Сервисы — ключи и входы в аккаунты. Плагины — умения, которые ставятся на колонку отдельно</p></div>
    <div class="actions"><button class="btn" data-net data-tip="Колонка достучится до облаков с вашими ключами и покажет время ответа">${icon("radio")}Проверить связь</button></div></header>
  <div class="seg cx-tabs" id="cx-tabs" role="tablist"><button type="button" role="tab" data-tab="svc">Сервисы<small id="cx-n-svc"></small></button><button type="button" role="tab" data-tab="pl">Плагины<small id="cx-n-pl"></small></button></div>
  <div id="conns" class="cx-list"></div>
  <div class="modal hidden" id="cn-modal" role="dialog" aria-modal="true" aria-labelledby="cn-mt"><div class="modal-bg" data-close></div><div class="modal-card cn-card" id="cn-card"></div></div>`;
  CONN.open = "";
  const view = $("#view");
  view.oninput = null;
  view.onchange = connChange;
  view.onclick = connClick;
  view.onkeydown = (e) => {
    if (page !== "connections") return; // обработчик висит на #view и после ухода со страницы
    const t = e.target.closest("[data-item]");
    if (t && e.target === t && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); connOpen(t.dataset.item); }
    if (e.key === "Escape" && CONN.open) connClose();
    if (e.key === "Enter" && e.target.matches("input[id^='k-']")) $(`[data-key="${e.target.id.slice(2)}"]`)?.click();
    if (e.key === "Enter" && e.target.id === "sl-code") $("#sl-in")?.click();
  };
  drawConns();
  plLoad().then(() => { if (page === "connections") drawConns(); });
  if (!PL.list) plCatalog(false).then(() => { if (page === "connections") drawConns(); });
}

function tileState(it) {
  if (it.kind === "pl") return plState(it.x);
  const net = CONN.net[it.id];
  if (net) return net.ok ? { cls: "ok", text: `${it.state} · ${net.ms} мс` } : { cls: "bad", text: net.error || "нет связи" };
  return { cls: it.bad ? "bad" : it.on ? "ok" : it.need ? "warn" : "", text: it.state };
}

export function connTile(it) {
  const st = tileState(it);
  const missing = it.kind === "pl" && !it.x.inst;
  const action = missing ? `${icon("plus")}Установить` : it.on ? `${icon("tune")}Настроить` : `${icon("plus")}Подключить`;
  const av = it.kind === "pl" ? plIcon(it.icon) : it.icon ? icon(it.icon) : esc(it.mono);
  return `<article class="card cn${it.on && !missing ? " on" : ""}${missing ? " off" : ""}" data-item="${esc(it.key)}" tabindex="0" role="button" aria-label="${esc(it.name)}">
    <div class="cn-h"><span class="char-av cn-av">${av}</span><h2>${esc(it.name)}</h2>${it.kind === "pl" && !it.x.official ? `<span class="cx-tag" data-tip="Неофициальный плагин">${icon("shield")}</span>` : ""}</div>
    <p class="cn-t">${it.about}</p>
    <div class="cn-f"><span class="st ${st.cls}">${esc(st.text)}</span><span class="spacer"></span><span class="char-edit">${action}</span></div>
  </article>`;
}

// plRow — строка установленного плагина: значок, что это, состояние, вкл/выкл, «Настроить».
// Не установленный — значок, что это, размер и «Установить».
function plRow(it) {
  const x = it.x, p = x.inst, st = plState(x);
  if (!p) return `<div class="cx-row add" data-item="${esc(it.key)}" tabindex="0" role="button" aria-label="${esc(it.name)} — подробнее">
    <span class="char-av cn-av">${plIcon(it.icon)}</span>
    <div class="cx-row-t"><b>${esc(it.name)}</b><small>${it.about}</small></div>
    <span class="cx-size">${x.size ? plSize(x.size) : ""}</span>
    <button class="btn sm" type="button" data-pl-install="${esc(x.repo)}" data-id="${esc(x.id)}" data-official="${x.official ? 1 : ""}">${icon("plus")}Установить</button>
  </div>`;
  return `<div class="cx-row${p.enabled ? "" : " off"}" data-item="${esc(it.key)}" tabindex="0" role="button" aria-label="${esc(it.name)} — настроить">
    <span class="char-av cn-av">${plIcon(it.icon)}</span>
    <div class="cx-row-t"><b>${esc(it.name)}${x.official ? "" : `<span class="plg-b warn">неофициальный</span>`}</b><small>${it.about}</small></div>
    <span class="st ${st.cls}">${esc(st.text)}</span>
    <div class="cx-acts"><label class="sw" data-tip="${p.enabled ? "Выключить" : "Включить"}"><input type="checkbox" data-pl-on="${esc(p.id)}"${p.enabled ? " checked" : ""} aria-label="${p.enabled ? "Выключить" : "Включить"} ${esc(it.name)}"><span class="tr"></span></label>
      <button class="btn sm" type="button" data-pl-open="${esc(it.key)}">${icon("tune")}Настроить</button>
      <button class="btn sm ghost danger" type="button" data-pl-del="${esc(p.id)}" aria-label="Удалить ${esc(it.name)}">${icon("trash")}<span>Удалить</span></button></div>
  </div>`;
}

function svcHtml(items) {
  return CONN_GROUPS.map(([g, title, sub]) => {
    const list = items.filter((it) => it.group === g).sort((a, b) => (b.on ? 1 : 0) - (a.on ? 1 : 0));
    if (!list.length) return "";
    const n = list.filter((it) => it.on).length;
    return `<section class="cn-group"><div class="cn-gh"><h2>${title}</h2><span class="meta">${n ? `подключено ${n} из ${list.length}` : esc(sub)}</span></div>
      <div class="chars cn-grid">${list.map(connTile).join("")}</div></section>`;
  }).join("");
}

function plHtml(items) {
  const inst = items.filter((it) => it.x.inst);
  const have = new Set(inst.map((it) => it.id));
  const offer = items.filter((it) => !it.x.inst && it.x.official && !have.has(it.id));
  const comm = items.filter((it) => !it.x.inst && !it.x.official);
  const wait = `<p class="note">${PL.loading || !PL.list ? "Загружаю каталог…" : ""}</p>`;
  return `<section class="cn-group"><div class="cn-gh"><h2>Установлены</h2></div>
      ${inst.length ? `<div class="card cx-rows">${inst.map(plRow).join("")}</div>`
        : `<div class="card plg-empty">${icon("puzzle")}<p class="muted">Пока ни одного плагина. Ядро Джарвиса лёгкое: нужные умения ставятся отдельно — выберите ниже.</p></div>`}</section>
    <section class="cn-group"><div class="cn-gh"><h2>Можно поставить</h2><span class="meta">официальные, от автора YaJarvis</span></div>
      ${offer.length ? `<div class="card cx-rows">${offer.map(plRow).join("")}</div>` : PL.list && !PL.loading && !PL.catErr ? `<p class="note">Все официальные плагины уже стоят.</p>` : PL.catErr ? "" : wait}</section>
    <section class="cn-group"><div class="cn-gh"><h2>От сообщества</h2><span class="meta">их пишут другие люди</span></div>
      <p class="note cx-warn">${icon("shield")}<span>Автор YaJarvis сторонние плагины не проверяет: ставьте то, чему доверяете. Хотите сделать свой — <a class="lnk" href="https://github.com/${esc(plRepo())}/blob/main/docs/PLUGINS.md" target="_blank" rel="noopener">инструкция</a>.</span></p>
      ${comm.length ? `<div class="card cx-rows">${comm.map(plRow).join("")}</div>` : PL.list && !PL.loading ? `<p class="note">Пока никто не опубликовал свой плагин.</p>` : ""}</section>
    ${PL.catErr && !PL.loading ? `<p class="note bad">Каталог плагинов загрузился не полностью: ${esc(PL.catErr)}</p>` : ""}
    <div class="cx-foot"><span>${PL.data?.free_mb ? `Свободно на колонке ${PL.data.free_mb} МБ` : ""}</span><span class="spacer"></span>
      <label class="btn sm" data-tip="Архив плагина .zip — например, скачанный из его релиза">${icon("upload")}Плагин из файла<input type="file" id="pl-file" accept=".zip,application/zip" hidden></label>
      <button class="btn sm ghost" type="button" id="pl-refresh"${PL.loading ? " disabled" : ""}>${icon("search")}Обновить каталог</button></div>`;
}

export function drawConns() {
  const box = $("#conns");
  if (!box) return;
  const all = connItems();
  const svc = all.filter((it) => it.kind === "svc"), pls = all.filter((it) => it.kind === "pl");
  $$("#cx-tabs [data-tab]").forEach((b) => { const on = b.dataset.tab === CONN.tab; b.classList.toggle("on", on); b.setAttribute("aria-selected", on); });
  const nSvc = svc.filter((it) => it.on).length, nPl = pls.filter((it) => it.x.inst).length;
  $("#cx-n-svc").textContent = nSvc ? String(nSvc) : "";
  $("#cx-n-pl").textContent = nPl ? String(nPl) : "";
  box.innerHTML = CONN.tab === "pl" ? plHtml(pls) : svcHtml(svc);
  if (CONN.open) connBody(CONN.open);
  // вход кодом: ждём, пока человек введёт код на сайте
  clearTimeout(drawConns.wait);
  if (svc.some((c) => c.wait)) drawConns.wait = setTimeout(async () => {
    if (page !== "connections") return;
    const was = { cg: S.chatgpt.signed_in, yt: S.youtube?.account?.signed_in, xm: S.xiaomi?.signed_in, sl: S.smartlife?.signed_in };
    await reload(); drawConns();
    if (!was.sl && S.smartlife?.signed_in) toast("Smart Life подключён — ищу устройства");
    if (!was.xm && S.xiaomi?.signed_in) toast("Xiaomi Home подключён — ищу устройства");
    if (!was.cg && S.chatgpt.signed_in) toast("ChatGPT подключён");
    if (!was.yt && S.youtube?.account?.signed_in) toast("YouTube подключён");
  }, 3000);
}

// pluginBlock — плагин внутри окна сервиса или плагина: версия, вкл/выкл, обновить, удалить.
function pluginBlock(p, c, own) {
  const upd = c?.version && p.version && c.version !== p.version;
  const meta = [p.version && `v${p.version}`, PL_KIND[p.kind] || p.kind, p.size && plSize(p.size), p.author].filter(Boolean);
  return `<div class="cx-plug"><div class="cx-plug-h"><span class="cx-plug-n">${icon("puzzle")}<b>${own ? "О плагине" : `Плагин «${esc(p.name)}»`}</b></span>
      <label class="sw" data-tip="${p.enabled ? "Выключить" : "Включить"}"><input type="checkbox" data-pl-on="${esc(p.id)}"${p.enabled ? " checked" : ""}><span class="tr"></span></label></div>
    <div class="plg-meta">${plBadge(p.official)}${meta.map((s) => `<span>${esc(s)}</span>`).join("")}${p.homepage ? `<a class="lnk" href="${esc(p.homepage)}" target="_blank" rel="noopener">${icon("ext")}код</a>` : ""}</div>
    ${p.error ? `<p class="note bad">${esc(p.error)}</p>` : p.status && p.enabled ? `<p class="note">${esc(p.status)}</p>` : ""}
    <div class="row">${upd ? `<button class="btn sm" type="button" data-pl-update="${esc(p.id)}">Обновить до ${esc(c.version)}</button>` : ""}<span class="spacer"></span>
      <button class="btn sm ghost danger" type="button" data-pl-del="${esc(p.id)}">Удалить плагин</button></div></div>`;
}

function head(av, name, st, extra = "") {
  return `<div class="cn-mh"><span class="char-av cn-av">${av}</span>
      <div class="char-n"><h2 id="cn-mt">${esc(name)}</h2><span class="st ${st.cls}">${esc(st.text)}</span></div>
      <button class="ib" type="button" data-close aria-label="Закрыть">${icon("x")}</button></div>${extra}`;
}

export function connBody(key) {
  const card = $("#cn-card");
  const it = connItems().find((x) => x.key === key);
  if (!card || !it) return;
  // поля, которые человек уже начал заполнять, перерисовка не стирает
  const keep = {};
  card.querySelectorAll("input:not([type=checkbox]), textarea").forEach((el) => { if (el.id && el.value) keep[el.id] = el.value; });
  const st = tileState(it);
  if (it.kind === "svc") {
    const av = it.icon ? icon(it.icon) : esc(it.mono);
    card.innerHTML = head(av, it.name, st) + `<p class="cn-about">${it.about}${it.more ? `. ${it.more}` : ""}</p>
        <div class="cn-body">${it.body}</div>
        ${it.drop ? `<div class="row cn-foot"><span class="spacer"></span>${it.drop}</div>` : ""}`;
  } else {
    const x = it.x, p = x.inst;
    if (!p) {
      card.innerHTML = head(plIcon(x.icon), x.name, st) + `<p class="cn-about">${esc(x.description || "Без описания")}</p>
        <div class="plg-meta">${plBadge(x.official)}${[x.version && `v${x.version}`, PL_KIND[x.kind] || x.kind, x.size && plSize(x.size), x.author, x.stars ? `★ ${x.stars}` : ""].filter(Boolean).map((s) => `<span>${esc(s)}</span>`).join("")}
          ${x.url ? `<a class="lnk" href="${esc(x.url)}" target="_blank" rel="noopener">${icon("ext")}код</a>` : ""}</div>
        <div class="row cn-foot"><span class="spacer"></span><button class="btn primary" type="button" data-pl-install="${esc(x.repo)}" data-id="${esc(x.id)}" data-official="${x.official ? 1 : ""}">${icon("plus")}Установить</button></div>`;
    } else {
      const fields = p.settings || [];
      const svcs = connDefs().filter((c) => c.plugin === p.id);
      const training = p.id === "training" && S.settings.wake_training !== undefined;
      const description = training ? "Меняет фразу активации или дообучает «Джарвис» по вашим записям." : p.description;
      card.innerHTML = head(plIcon(p.icon), p.name, st) + `<p class="cn-about">${esc(description || "")}</p>
        ${fields.length ? `<div class="cn-body plg-form">${fields.map((s) => plField(p, s)).join("")}</div>
          <div class="row"><span class="spacer"></span><button class="btn primary" type="button" data-pl-save="${esc(p.id)}">Сохранить</button></div>` : ""}
        ${svcs.length ? `<div class="cx-links"><b>${svcs.length > 1 ? "Входы и ключи этого плагина" : "Вход и настройки"}</b>${svcs.map((c) => `<button class="cx-link" type="button" data-goto="svc:${esc(c.id)}">
            <span class="char-av cn-av">${c.icon ? icon(c.icon) : esc(c.mono)}</span><span>${esc(c.name)}<small>${esc(c.state.replace(/<[^>]+>/g, ""))}</small></span>${icon("right")}</button>`).join("")}</div>`
          : training ? (S.settings.wake_training ? '<div><a class="btn primary" href="#training">Открыть обучение</a></div>' : "")
          : fields.length ? "" : `<p class="note">У этого плагина нет настроек: он просто работает. Выключить или удалить его можно ниже.</p>`}
        ${pluginBlock(p, plCatItem(p), true)}`;
    }
  }
  for (const [id, val] of Object.entries(keep)) { const el = card.querySelector("#" + id); if (el) el.value = val; }
}

function modalShow() {
  const m = $("#cn-modal");
  m.classList.remove("hidden", "out"); void m.offsetWidth; m.classList.add("in");
}

// connOpen — открыть окно: ключ «svc:youtube» / «pl:weather» или просто id сервиса (ссылки #connections/youtube).
export function connOpen(key) {
  if (!key) return;
  if (key === "plugins" || key === "services") { CONN.tab = key === "plugins" ? "pl" : "svc"; return drawConns(); }
  if (!key.includes(":")) key = connDefs().some((c) => c.id === key) ? "svc:" + key : "pl:" + key;
  if (!connItems().some((x) => x.key === key)) return;
  const tab = key.startsWith("pl:") ? "pl" : "svc";
  if (CONN.tab !== tab) { CONN.tab = tab; CONN.open = ""; drawConns(); }
  CONN.open = key;
  connBody(key);
  modalShow();
  // на телефоне не фокусируем поле: иначе сразу выпрыгивает клавиатура и закрывает окно
  if (matchMedia("(hover: hover)").matches) setTimeout(() => $("#cn-card input:not([type=checkbox]), #cn-card textarea, #cn-card .btn.primary")?.focus(), 60);
}

// connWarn — предупреждение перед неофициальным плагином в том же окне; true — ставим.
function connWarn(name) {
  if (plWarned()) return Promise.resolve(true);
  return new Promise((done) => {
    const back = CONN.open;
    CONN.open = "";
    $("#cn-card").innerHTML = plWarnHtml(name);
    modalShow();
    const finish = async (yes) => {
      if (yes && $("#pl-warn-ok")?.checked) await plMarkWarned();
      $("#cn-modal").removeEventListener("click", onClick, true);
      if (back && yes) { CONN.open = back; connBody(back); } else connClose();
      done(yes);
    };
    const onClick = (e) => {
      if (e.target.closest("#pl-warn-yes")) { e.stopPropagation(); finish(true); }
      else if (e.target.closest("[data-close]")) { e.stopPropagation(); finish(false); }
    };
    $("#cn-modal").addEventListener("click", onClick, true);
  });
}

export function connClose() {
  const m = $("#cn-modal");
  CONN.open = "";
  m.classList.remove("in"); m.classList.add("out");
  setTimeout(() => { if (m.classList.contains("out")) m.classList.add("hidden"); }, 260);
}

export async function connNetTest(btn) {
  await act(btn, async () => {
    if ($("#net-res")) $("#net-res").innerHTML = `<div class="nr"><span class="spin"></span>Проверяю…</div>`;
    let r;
    try { r = await api("POST", "/api/test/net"); } catch (err) { if ($("#net-res")) $("#net-res").innerHTML = ""; throw err; }
    CONN.net = {};
    for (const [name, x] of Object.entries(r)) CONN.net[name] = x;
    drawConns();
    if ($("#net-res")) $("#net-res").innerHTML = Object.entries(r).map(([name, x]) => `<div class="nr ${x.ok ? "ok" : "bad"}"><b>${NET_NAMES[name] || esc(name)}</b>${x.ok ? `работает · ${x.ms} мс` : esc(x.error || "нет связи")}</div>`).join("");
    const bad = Object.entries(r).filter(([, x]) => !x.ok).map(([n]) => NET_NAMES[n] || n);
    if (!Object.keys(r).length) toast("Нечего проверять — нет ни одного ключа", true);
    else if (bad.length) toast(`Нет связи: ${bad.join(", ")}`, true);
    else toast("Связь со всеми есть");
  });
}

export async function connClick(e) {
  if (page !== "connections") return;
  const t = e.target;
  const redraw = async () => { await reload(); drawConns(); };
    if(t.closest("[data-ha-remove]")){t.closest("[data-ha-room]").remove();return;}
  if (t.closest("[data-close]")) return connClose();
  const tab = t.closest("#cx-tabs [data-tab]");
  if (tab) { CONN.tab = tab.dataset.tab; drawConns(); return; }
  const go = t.closest("[data-goto]");
  if (go) { connClose(); setTimeout(() => connOpen(go.dataset.goto), 280); return; }
  if (t.closest("[data-item] .sw")) return; // переключатель в строке плагина ловит connChange
  const plOpen = t.closest("[data-pl-open]");
  if (plOpen) return connOpen(plOpen.dataset.plOpen);
  if (t.closest("#pl-refresh")) { const b = t.closest("#pl-refresh"); b.disabled = true; await plCatalog(true); drawConns(); return; }
  const after = async () => { await reload(); drawConns(); };
  const inst = t.closest("[data-pl-install]");
  if (inst) {
    e.stopPropagation();
    if (!inst.dataset.official && !(await connWarn(inst.dataset.id))) return;
    return plInstall(inst, inst.dataset.plInstall, inst.dataset.id, after);
  }
  const upd = t.closest("[data-pl-update]");
  if (upd) return plUpdate(upd, upd.dataset.plUpdate, after);
  const del = t.closest("[data-pl-del]");
  if (del) return plRemove(del, del.dataset.plDel, async () => { const svc = CONN.open.startsWith("svc:"); if (!svc) connClose(); await after(); });
  const save = t.closest("[data-pl-save]");
  if (save) return plSave(save, save.dataset.plSave, plValues($("#cn-card")), after);
  if (t.matches("input[data-vpn]")) {
    const svc = t.dataset.vpn, on = t.checked;
    try { await api("PUT", "/api/settings", { vpn: { [svc]: on } }); delete CONN.net[svc]; await redraw(); toast(`${VPN_NAMES.find(([id]) => id === svc)?.[1] || svc}: ${on ? "через VPN" : "напрямую"}`); }
    catch (err) { t.checked = !on; toast(err.message, true); }
    return;
  }
  const gk = t.closest("[data-gk-remove]");
  if (gk) { if (confirm("Убрать этот ключ Google с колонки?")) act(gk, async () => { await api("PUT", "/api/gemini-keys", { remove: +gk.dataset.gkRemove }); await redraw(); }, "Ключ убран"); return; }
  const ek = t.closest("[data-ek-remove], [data-ek-reset]");
  if (ek) {
    const rm = ek.dataset.ekRemove !== undefined, i = +(rm ? ek.dataset.ekRemove : ek.dataset.ekReset);
    if (rm && !confirm(`Убрать ключ ${i + 1} с колонки?`)) return;
    return act(ek, async () => { await api("PUT", "/api/eleven-keys", rm ? { remove: i } : { reset: i }); await redraw(); }, rm ? "Ключ убран" : "Ключ снова в работе");
  }
  const tile = t.closest("[data-item]");
  if (tile) return connOpen(tile.dataset.item);
  const net = t.closest("[data-net]");
  if (net) return connNetTest(net);
  const b = t.closest("[data-key]"), c = t.closest("[data-clear]");
  if (b) {
    const val = $("#k-" + b.dataset.key).value.trim();
    if (!val) return toast("Сначала вставьте ключ в поле", true);
    return act(b, async () => { await api("PUT", "/api/keys", { [b.dataset.key]: val }); $("#k-" + b.dataset.key).value = ""; delete CONN.net[b.dataset.key]; await redraw(); }, (b.dataset.key === "elevenlabs" && (S.eleven_keys || []).length > 1) || (b.dataset.key === "gemini" && (S.gemini_keys || []).length > 1) ? "Ключ добавлен в запас" : "Ключ сохранён");
  }
  if (c) { if (confirm(c.dataset.clear === "elevenlabs" && (S.eleven_keys || []).length > 1 ? "Убрать все ключи ElevenLabs с колонки?" : c.dataset.clear === "gemini" && (S.gemini_keys || []).length > 1 ? "Убрать все ключи Google с колонки?" : "Убрать этот ключ с колонки?")) act(c, async () => { await api("PUT", "/api/keys", { [c.dataset.clear]: "" }); delete CONN.net[c.dataset.clear]; await redraw(); }); return; }
  const btn = t.closest("button[id]");
  if (!btn) return;
  switch (btn.id) {
    case "ha-discover-rooms": return act(btn,async()=>{
      const r=await api("POST","/api/home-assistant/rooms/discover",{});
      if(!r.rooms?.length){toast("Автоматическое определение комнат недоступно. Можно выбрать готовые сценарии уборки.",true);return;}
      const catalog=await api("GET","/api/home-assistant");CONN.haEntities=catalog.entities || [];
      $("#ha-rooms").innerHTML=r.rooms.map(haRoomRow).join("");toast(`Найдено комнат: ${r.rooms.length}. Нажмите «Проверить и сохранить».`);
    });
    case "ha-add-room": $("#ha-rooms").insertAdjacentHTML("beforeend",haRoomRow()); return;
    case "ha-load": return act(btn,async()=>{
      const r=await api("GET","/api/home-assistant");CONN.haEntities=r.entities || [];
      const rooms=$$("[data-ha-room]").map(el=>({...JSON.parse(el.dataset.haBinding || "{}"),name:el.querySelector(".ha-name").value,vacuum:el.querySelector(".ha-vacuum").value,entity:el.querySelector(".ha-entity").value,room_ids:el.querySelector(".ha-ids").value}));
      $("#ha-rooms").innerHTML=rooms.map(haRoomRow).join("");
      toast(`Получено устройств и действий: ${CONN.haEntities.length}`);
    });
    case "ha-save": return act(btn,async()=>{
      const rooms=$$("[data-ha-room]").map(el=>({...JSON.parse(el.dataset.haBinding || "{}"),name:el.querySelector(".ha-name").value.trim(),vacuum:el.querySelector(".ha-vacuum").value,entity:el.querySelector(".ha-entity").value,room_ids:el.querySelector(".ha-ids").value.trim()}));
      await api("PUT","/api/home-assistant",{url:$("#ha-url").value,token:$("#ha-token").value,rooms});
      $("#ha-token").value="";await redraw();
    },"Home Assistant подключён");
    case "ha-disconnect": return act(btn,async()=>{await api("DELETE","/api/home-assistant");CONN.haEntities=[];await redraw();},"Home Assistant отключён");
    case "cg-in": return act(btn, async () => { await api("POST", "/api/chatgpt/login"); await redraw(); });
    case "cg-out": if (confirm("Выйти из ChatGPT на колонке?")) act(btn, async () => { await api("DELETE", "/api/chatgpt"); await redraw(); }); return;
    case "sl-in": {
      const code = $("#sl-code").value.trim();
      if (!code) return toast("Сначала вставьте код пользователя из Smart Life", true);
      return act(btn, async () => { await api("POST", "/api/smartlife/login", { user_code: code }); await redraw(); });
    }
    case "sl-refresh": return act(btn, async () => { const r = await api("POST", "/api/smartlife/refresh"); await redraw(); toast(`Устройств: ${r.devices}`); });
    case "sl-out": if (confirm("Выйти из Smart Life на колонке?")) act(btn, async () => { await api("DELETE", "/api/smartlife"); await redraw(); }); return;
    case "xm-in": return act(btn, async () => { await api("POST", "/api/xiaomi/login"); await redraw(); });
    case "xm-refresh": return act(btn, async () => { const r = await api("POST", "/api/xiaomi/refresh"); await redraw(); toast(`Устройств: ${r.devices}`); });
    case "xm-out": if (confirm("Выйти из Xiaomi Home на колонке?")) act(btn, async () => { await api("DELETE", "/api/xiaomi"); await redraw(); }); return;
    case "ya-in": return act(btn, async () => { await api("POST", "/api/youtube/login"); await redraw(); });
    case "ya-out": if (confirm("Выйти из YouTube на колонке?")) act(btn, async () => { await api("DELETE", "/api/youtube/login"); await redraw(); }); return;
    case "yt-save": {
      const txt = $("#yt-cookies").value.trim();
      if (!txt) return toast("Сначала вставьте текст файла cookies.txt", true);
      return act(btn, async () => { await api("PUT", "/api/youtube", { cookies: txt }); $("#yt-cookies").value = ""; await redraw(); }, "YouTube принял вход");
    }
    case "yt-clear": if (confirm("Убрать вход в YouTube с колонки?")) act(btn, async () => { await api("DELETE", "/api/youtube"); await redraw(); }); return;
    case "vless-save": return act(btn, async () => { await api("PUT", "/api/vless", { link: $("#vless").value }); $("#vless").value = ""; await redraw(); }, "Ссылка сохранена — проверьте связь");
    case "vless-clear": if (confirm("Убрать VPN? Колонка пойдёт в интернет напрямую.")) act(btn, async () => { await api("PUT", "/api/vless", { link: "" }); await redraw(); }); return;
  }
}

// connChange — переключатели плагинов и файл плагина (select и checkbox ловим на change).
export async function connChange(e) {
  if (page !== "connections") return;
  const t = e.target;
  if (t.dataset.plOn) return plEnable(t.dataset.plOn, t.checked, async () => { await reload(); drawConns(); });
  if (t.id === "pl-file" && t.files?.[0]) {
    const f = t.files[0];
    t.value = "";
    if (!(await connWarn(f.name))) return;
    await plUpload(f, async (p) => { await reload(); drawConns(); if (p?.id) connOpen("pl:" + (p.official ? p.id : (p.source || "file") + "/" + p.id)); });
  }
}
