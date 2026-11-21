// Ассистент: голос, модель, характер.
import { $, $$, act, api, esc, icon, sec, setDirty, tip, toast } from "./core.js";
import { ledPickerBind, ledPickerHtml, paintLeds } from "./leds.js";
import { S, avatar, classicEnabled, go, isClassicProfile, reload, route, sttReady, ttsReady, voiceEnabled } from "./main.js";

export let previewSeq = 0;

const BUILTIN_LLM = ["", "openai", "chatgpt", "gemini", "grok"];
const EL_STT_MODELS = [
  ["scribe_v3_live", "Scribe v3 Live — определяет конец мысли"],
  ["scribe_v2_realtime", "Scribe v2 Realtime"],
];
const OA_STT_MODELS = [
  ["gpt-4o-mini-transcribe", "GPT-4o mini Transcribe"],
  ["gpt-4o-transcribe", "GPT-4o Transcribe"],
  ["whisper-1", "Whisper v1"],
];
const EL_TTS_MODELS = [
  "eleven_v4_turbo",
  "eleven_v4",
  "eleven_v3_conversational",
  "eleven_v3",
  "eleven_flash_v2_5",
  "eleven_multilingual_v2",
];
const OA_TTS_MODELS = [
  ["gpt-4o-mini-tts", "GPT-4o mini TTS"],
  ["tts-1", "TTS-1"],
  ["tts-1-hd", "TTS-1 HD"],
];

function optionsWithCurrent(list, current, extraDisabled = false) {
  const opts = [...list];
  if (current && !opts.some(([id]) => id === current)) {
    opts.push([current, extraDisabled ? `${current} (отключён)` : current]);
  }
  return opts;
}

// ---------------------------------------------------------------- ассистент
export function viewProfile(id) {
  const isNew = id === "new";
  const base = S.profiles.find((x) => x.id === id);
  if (!isNew && !base) { go("#home"); return; }
  const p = structuredClone(base || { id: "", name: "", color: (S.profiles.length % 6) + 1, mode: "live", engine: "", voice: "", effort: "low", priority: true, web_search: true, idle_s: 8, prompt: S.prompts[0]?.id || "" });
  p.led ||= { ...(S.settings.led || {}) }; p.led.brightness ||= 45;
  p.volume ||= S.settings.volume || 40; p.city ??= S.settings.city || ""; p.timezone ||= S.settings.timezone || "UTC";
  if (p.engine === "live") p.engine = "openai";

  const classicInitial = isClassicProfile(p);
  if (classicInitial) {
    p.tts_engine ||= p.engine === "openai" ? "openai" : "elevenlabs";
    p.engine = "classic";
    p.stt_engine ||= "elevenlabs";
    p.llm_engine ||= p.llm_plugin || "";
  } else {
    p.stt_engine ||= voiceEnabled("elevenlabs") ? "elevenlabs" : voiceEnabled("openai") ? "openai" : ((S.stt_plugins || [])[0]?.id || "elevenlabs");
    p.tts_engine ||= voiceEnabled("elevenlabs") ? "elevenlabs" : voiceEnabled("openai") ? "openai" : ((S.tts_plugins || [])[0]?.id || "elevenlabs");
    p.llm_engine ||= p.llm_plugin || "";
  }
  p.llm_model ||= p.backend_model || "";
  if (p.engine === "classic" && (!p.llm_engine || ["openai", "chatgpt"].includes(p.llm_engine))) {
    p.backend_model = p.llm_model || S.backend_models[0] || "";
  }
  p.tts_model ||= p.tts_engine === "openai" ? "gpt-4o-mini-tts" : "eleven_flash_v2_5";
  p.stt_model ||= p.stt_engine === "openai" ? "gpt-4o-mini-transcribe" : "scribe_v2_realtime";

  const active = p.id === S.active;
  const textPlugins = [...(S.text_plugins || [])];
  if (p.llm_plugin && !textPlugins.some((plugin) => plugin.id === p.llm_plugin)) {
    textPlugins.push({ id: p.llm_plugin, name: p.llm_plugin + " (отключён)" });
  }

  const sttChoices = [];
  if (voiceEnabled("elevenlabs") || p.stt_engine === "elevenlabs") sttChoices.push(["elevenlabs", voiceEnabled("elevenlabs") ? "ElevenLabs Scribe" : "ElevenLabs (отключён)"]);
  if (voiceEnabled("openai") || p.stt_engine === "openai") sttChoices.push(["openai", voiceEnabled("openai") ? "OpenAI" : "OpenAI (отключён)"]);
  for (const pl of (S.stt_plugins || [])) sttChoices.push([pl.id, pl.name]);
  const sttOpts = optionsWithCurrent(sttChoices, p.stt_engine, !sttReady(p.stt_engine));

  const llmChoices = [["", "Автовыбор (OpenAI / Gemini / Grok / ChatGPT)"]];
  for (const [lid, label] of [["openai", "OpenAI"], ["chatgpt", "ChatGPT по подписке"], ["gemini", "Google Gemini"], ["grok", "xAI Grok"]]) {
    if (voiceEnabled(lid) || p.llm_engine === lid) llmChoices.push([lid, voiceEnabled(lid) ? label : `${label} (отключён)`]);
  }
  for (const pl of (S.text_plugins || [])) llmChoices.push([pl.id, pl.name]);
  const llmOpts = optionsWithCurrent(llmChoices, p.llm_engine, true);

  const ttsChoices = [];
  if (voiceEnabled("elevenlabs") || p.tts_engine === "elevenlabs") ttsChoices.push(["elevenlabs", voiceEnabled("elevenlabs") ? "ElevenLabs" : "ElevenLabs (отключён)"]);
  if (voiceEnabled("openai") || p.tts_engine === "openai") ttsChoices.push(["openai", voiceEnabled("openai") ? "OpenAI" : "OpenAI (отключён)"]);
  for (const pl of (S.tts_plugins || [])) ttsChoices.push([pl.id, pl.name]);
  const ttsOpts = optionsWithCurrent(ttsChoices, p.tts_engine, !ttsReady(p.tts_engine));

  $("#view").innerHTML = `<header class="ph who"><span id="pav" class="ringbox">${avatar(p, true)}</span><div><h1>${isNew ? "Новый ассистент" : esc(p.name)}</h1><p class="sub">${isNew ? "Свой голос, модель и характер" : active ? "Активный профиль" : "Не активен"}</p></div></header>
  ${sec("Основное", "Как ассистент выглядит в списке и кем он себя считает",
    `<div class="grid g2"><div class="f"><label>Имя ${tip("Как ассистент называется в списке слева. На слово активации не влияет.")}</label><input type="text" id="p-name" maxlength="40" value="${esc(p.name)}" placeholder="Например, Джарвис"></div>
      <div class="f"><label>Цвет</label><div class="colors" id="p-color">${[1, 2, 3, 4, 5, 6].map((c) => `<button class="av c${c}${p.color === c ? " on" : ""}" data-c="${c}" type="button" aria-label="Цвет ${c}"></button>`).join("")}</div></div></div>
    <div class="f"><label>Характер ${tip("Системный промпт: кто такой Джарвис и как он общается. Характеры редактируются в разделе «Характеры».")}</label><select id="p-prompt">${S.prompts.map((c) => `<option value="${esc(c.id)}"${c.id === p.prompt ? " selected" : ""}>${esc(c.name)}</option>`).join("")}</select></div>`)}
  ${sec("Мозг", "Выберите живой разговор или свой пайплайн: распознавание речи → текстовая модель → озвучка",
    `<p class="note hidden" id="p-no-engine">Установите и включите голосовой плагин в <a href="#connections/plugins">«Подключениях»</a>.</p>
    <div class="choice" id="p-engine">
      <button class="opt" data-engine="classic" type="button"><b>Свой пайплайн (STT → LLM → TTS)</b><small>Соберите цепочку из разных провайдеров и плагинов: распознавание речи, текстовая модель (включая OpenRouter / плагины) и озвучка</small></button>
      <button class="opt" data-engine="eleven_realtime" type="button"><b>ElevenLabs — живой разговор</b><small>Eleven Realtime Mini или Max: распознавание, мозг, голос и перебивания вместе. Нужен только ключ ElevenLabs</small></button>
      <button class="opt" data-engine="chatgpt" type="button"><b>GPT-Live по подписке</b><small>Ваш ChatGPT Plus или Pro: минуты не оплачиваются, есть лимиты подписки. Нужен вход в «Подключениях»</small></button>
      <button class="opt" data-engine="openai" type="button"><b>GPT-Live</b><small>OpenAI по ключу API. Около 5 центов за минуту разговора</small></button>
      <button class="opt" data-engine="gemini" type="button"><b>Gemini Live</b><small>Google. Примерно вдвое дешевле, есть бесплатный уровень. Нужен ключ Gemini в «Подключениях»</small></button>
      <button class="opt" data-engine="grok" type="button"><b>Grok Voice</b><small>xAI. Около 5 центов за минуту, сам ищет в интернете. Нужен ключ xAI в «Подключениях»</small></button>
    </div>
    <div class="eng stack" data-for="classic">
      <div class="grid g2">
        <div class="f"><label for="p-stt-engine">1. Распознавание речи (STT)</label>
          <select id="p-stt-engine">${sttOpts.map(([v, n]) => `<option value="${esc(v)}"${v === p.stt_engine ? " selected" : ""}>${esc(n)}</option>`).join("")}</select></div>
        <div class="f">
          <label for="p-stt-model-el">Модель STT</label>
          <select id="p-stt-model-el">${optionsWithCurrent(EL_STT_MODELS, p.stt_engine === "elevenlabs" ? p.stt_model : "").map(([m, n]) => `<option value="${esc(m)}"${m === (p.stt_model || "scribe_v2_realtime") ? " selected" : ""}>${esc(n)}</option>`).join("")}</select>
          <select id="p-stt-model-oa" class="hidden">${optionsWithCurrent(OA_STT_MODELS, p.stt_engine === "openai" ? p.stt_model : "").map(([m, n]) => `<option value="${esc(m)}"${m === (p.stt_model || "gpt-4o-mini-transcribe") ? " selected" : ""}>${esc(n)}</option>`).join("")}</select>
          <input id="p-stt-model-custom" class="hidden" type="text" maxlength="160" value="${esc(!["elevenlabs", "openai"].includes(p.stt_engine) ? p.stt_model : "")}" placeholder="По умолчанию плагина" autocomplete="off">
        </div>
      </div>
      <div class="grid g2">
        <div class="f"><label for="p-llm-engine">2. Текстовая модель (LLM)</label>
          <select id="p-llm-engine">${llmOpts.map(([v, n]) => `<option value="${esc(v)}"${v === p.llm_engine ? " selected" : ""}>${esc(n)}</option>`).join("")}</select></div>
        <div class="f hidden" id="p-llm-custom-box"><label for="p-llm-model-custom">Модель (необязательно)</label>
          <input id="p-llm-model-custom" type="text" maxlength="160" value="${esc(!["", "openai", "chatgpt"].includes(p.llm_engine) ? p.llm_model : "")}" placeholder="deepseek/deepseek-chat-v3-0324" autocomplete="off"></div>
      </div>
    </div>
    <div class="eng" data-for="eleven_realtime"><div class="f half"><label>Разговорная модель</label><select id="p-eleven-rtmodel">${["eleven_realtime_v1_mini", "eleven_realtime_v1_max"].map((m) => `<option value="${m}"${m === p.live_model ? " selected" : ""}>${m.endsWith("max") ? "Realtime v1 Max — сложные задачи и инструменты" : "Realtime v1 Mini — быстрый разговор"}</option>`).join("")}</select></div></div>
    <div class="eng" data-for="gemini"><div class="f half"><label>Модель ${tip("С бесплатным ключом из AI Studio все эти модели работают без оплаты, но у каждой свой дневной лимит. Когда он кончается, Джарвис сам переходит на следующую модель в списке.")}</label><select id="p-gmodel">${(S.gemini_model_info?.length ? S.gemini_model_info : S.gemini_models.map((id) => ({ id, name: id, note: "" }))).map((m) => `<option value="${esc(m.id)}"${m.id === p.live_model ? " selected" : ""}>${esc(m.name)}${m.note ? ` — ${esc(m.note)}` : ""}</option>`).join("")}</select></div></div>
    <div id="p-openai-model-box" class="hidden"><div class="f half"><label>Текстовая модель ${tip("Чем умнее модель, тем дольше она думает. Для голосового помощника обычно хватает самой быстрой.")}</label><select id="p-model">${S.backend_models.map((m) => `<option${m === p.backend_model ? " selected" : ""}>${esc(m)}</option>`).join("")}</select></div>
      <div class="f"><label>Сколько думать ${tip("Доступные уровни зависят от выбранной текстовой модели: не все модели поддерживают «не думать». Немного — быстрее, средне и долго — для сложных вопросов.")}</label><div class="seg" id="p-effort">${S.efforts.map((e) => `<button type="button" data-v="${e}" class="${e === p.effort ? "on" : ""}">${({ none: "не думать", low: "немного", medium: "средне", high: "долго" })[e] || e}</button>`).join("")}</div></div>
      <label class="sw"><input type="checkbox" id="p-priority"${p.priority ? " checked" : ""}><span class="tr"></span>Приоритет ${tip("Запросы обрабатываются в быстрой очереди OpenAI: ответ быстрее, но стоит дороже.")}</label></div>
    ${textPlugins.length ? `<div class="eng f" data-for="openai chatgpt gemini grok eleven_realtime"><label for="p-llm-plugin">Текстовая модель из плагина</label>
      <select id="p-llm-plugin"><option value="">По настройкам ассистента</option>${textPlugins.map((plugin) => `<option value="${esc(plugin.id)}"${p.llm_plugin === plugin.id ? " selected" : ""}>${esc(plugin.name)}</option>`).join("")}</select>
      <small>Используется в переписке и при ответе голосовому плагину.</small></div>` : ""}
    <div class="eng stack" data-for="classic">
      <div class="grid g2">
        <div class="f"><label for="p-tts-engine">3. Озвучка (TTS)</label>
          <select id="p-tts-engine">${ttsOpts.map(([v, n]) => `<option value="${esc(v)}"${v === p.tts_engine ? " selected" : ""}>${esc(n)}</option>`).join("")}</select></div>
        <div class="f">
          <label for="p-tts-model-el">Модель озвучки</label>
          <select id="p-tts-model-el">${optionsWithCurrent(EL_TTS_MODELS.map((m) => [m, m]), p.tts_engine === "elevenlabs" ? p.tts_model : "").map(([m, n]) => `<option value="${esc(m)}"${m === (p.tts_model || "eleven_flash_v2_5") ? " selected" : ""}>${esc(n)}</option>`).join("")}</select>
          <select id="p-tts-model-oa" class="hidden">${optionsWithCurrent(OA_TTS_MODELS, p.tts_engine === "openai" ? p.tts_model : "").map(([m, n]) => `<option value="${esc(m)}"${m === (p.tts_model || "gpt-4o-mini-tts") ? " selected" : ""}>${esc(n)}</option>`).join("")}</select>
          <input id="p-tts-model-custom" class="hidden" type="text" maxlength="160" value="${esc(!["elevenlabs", "openai"].includes(p.tts_engine) ? p.tts_model : "")}" placeholder="По умолчанию плагина" autocomplete="off">
        </div>
      </div>
    </div>
    <div id="p-el-voices-bar" class="hidden"><p class="note">Голос выбирается ниже из вашего аккаунта ElevenLabs.</p><button class="btn" id="p-el-refresh" type="button">Обновить голоса ElevenLabs</button></div>
    <div class="stack">
      <label class="sw"><input type="checkbox" id="p-web"${p.web_search ? " checked" : ""}><span class="tr"></span>Поиск в интернете ${tip("Для погоды, новостей, курсов и всего свежего. Без него Джарвис знает только то, чему его научили.")}</label></div>
    <div class="f narrow"><label>Конец разговора, с ${tip("Если вы оба молчите столько секунд, разговор закрывается и колонка снова ждёт «Джарвис». Пока разговор открыт, звать по имени не нужно.")}</label><input type="number" id="p-idle" min="2" max="60" step="1" value="${p.idle_s}"></div>`)}
  ${sec("Голос", "Кнопка рядом с голосом — колонка скажет им пробную фразу. Первый раз это около цента, дальше фраза играет с самой колонки бесплатно",
    `<div id="voices" class="vgroups"></div>
    <div id="p-tts-voice-box" class="hidden row">
      <div class="f half"><label for="p-tts-voice-custom">ID голоса (или оставьте пустым по умолчанию)</label>
        <input id="p-tts-voice-custom" type="text" maxlength="128" value="${esc(p.tts_voice || "")}" placeholder="Например, голос из плагина" autocomplete="off"></div>
      <button class="btn" id="p-tts-voice-test" type="button">Послушать</button>
    </div>
    <details id="v-custom"><summary>Создать свой голос по описанию</summary><div class="inner">
      <div class="f"><label>Название</label><input type="text" id="v-name" placeholder="Например, Бархатный"></div>
      <div class="f"><label>Описание ${tip("Тембр, возраст, манера, темп, эмоции. Подражать конкретным людям и персонажам OpenAI не даёт, но описать манеру можно. Создание бесплатное.")}</label><textarea id="v-prompt" placeholder="Низкий спокойный мужской голос, лет сорока, говорит неторопливо, с лёгкой иронией…"></textarea></div>
      <div><button class="btn" id="v-create" type="button">Создать голос</button></div></div></details>
    <div class="f"><label>Громкость голоса ${tip("Насколько голос Джарвиса громче или тише остального звука колонки. Общая громкость колонки — на главной: голос меняется вместе с ней, но мягче — на тихой колонке его всё равно слышно, а на громкой он не орёт громче Алисы. Голосом: «Джарвис, громче/тише».")}<b id="p-vol-v" class="val">${p.volume}%</b></label><input type="range" id="p-vol" min="10" max="100" step="5" value="${p.volume}"></div>`)}
  ${sec("Подсветка", "Цвет кольца у этого ассистента. Нажмите цвет — колонка покажет его на 3 секунды",
    `<div id="p-led">${ledPickerHtml(p.led, "p")}</div>
    <div class="f"><label>Яркость <b id="p-br-v" class="val">${p.led.brightness}%</b></label><input type="range" id="p-br" min="5" max="100" step="5" value="${p.led.brightness}"></div>
    ${S.settings.led?.enabled === false ? `<p class="note">Сейчас подсветка выключена для всех — включается на странице «Колонка».</p>` : ""}`)}
  <div class="savebar"><button class="btn primary" id="p-save">${isNew ? "Создать" : "Сохранить"}</button>
    ${!isNew && !active ? `<button class="btn" id="p-activate" data-tip="Этот ассистент будет отвечать на «Джарвис»">Сделать активным</button>` : ""}
    ${!isNew && !active ? `<button class="btn ghost danger" id="p-del">Удалить</button>` : ""}<span class="dirty hidden" id="dirty">есть несохранённые изменения</span></div>`;

  let revision = 0;
  const mark = () => { revision++; setDirty(true); $("#dirty").classList.remove("hidden"); };
  $("#p-llm-plugin")?.addEventListener("change", mark);
  const effortOptions = (model) => S.model_efforts?.[model] || S.efforts;
  const refreshEffortOptions = () => {
    const allowed = effortOptions($("#p-model").value);
    if (!allowed.includes(p.effort)) { p.effort = "low"; mark(); }
    $$("#p-effort button").forEach((b) => {
      b.style.display = allowed.includes(b.dataset.v) ? "" : "none";
      b.classList.toggle("on", b.dataset.v === p.effort);
    });
  };
  refreshEffortOptions();
  $("#p-model").addEventListener("change", () => { p.backend_model = $("#p-model").value; refreshEffortOptions(); mark(); });
  $("#view").oninput = mark;
  $("#view").onchange = mark;
  $("#p-name").addEventListener("input", (e) => { p.name = e.target.value; $("#pav").innerHTML = avatar(p, true); });
  $("#p-color").addEventListener("click", (e) => { const b = e.target.closest("button"); if (!b) return; p.color = +b.dataset.c; $$("#p-color button").forEach((x) => x.classList.toggle("on", x === b)); $("#pav").innerHTML = avatar(p, true); mark(); });
  $("#p-vol").addEventListener("input", (e) => ($("#p-vol-v").textContent = e.target.value + "%"));
  ledPickerBind($("#p-led"), p.led, "p", () => mark());
  $("#p-br").addEventListener("input", (e) => { p.led.brightness = +e.target.value; $("#p-br-v").textContent = e.target.value + "%"; paintLeds($("#p-led"), p.led, "p"); });
  $("#p-br").addEventListener("change", () => { mark(); if (S.speaker.leds) api("POST", "/api/leds/preview", { mode: "listening", color: p.led.listening, brightness: p.led.brightness }).catch(() => {}); });
  $("#p-effort").addEventListener("click", (e) => { const b = e.target.closest("button"); if (!b) return; p.effort = b.dataset.v; $$("#p-effort button").forEach((x) => x.classList.toggle("on", x === b)); mark(); });

  const currentSttModel = () => {
    if (p.stt_engine === "elevenlabs") return $("#p-stt-model-el").value;
    if (p.stt_engine === "openai") return $("#p-stt-model-oa").value;
    return $("#p-stt-model-custom").value.trim();
  };
  const currentTtsModel = () => {
    if (p.tts_engine === "elevenlabs") return $("#p-tts-model-el").value;
    if (p.tts_engine === "openai") return $("#p-tts-model-oa").value;
    return $("#p-tts-model-custom").value.trim();
  };

  let elevenVoicesList = [], elevenLoaded = false, elevenLoading = false, elevenVoiceError = "";
  const pluginVoices = new Map();
  const effectiveTtsEngine = () => p.engine === "classic" ? p.tts_engine : p.engine;

  const loadElevenVoices = async () => {
    if (elevenLoaded || elevenLoading) return; elevenLoading = true;
    try { const r = await api("GET", "/api/elevenlabs/voices"); elevenVoicesList = r.voices || []; elevenVoiceError = ""; }
    catch (err) { elevenVoiceError = err.message; }
    finally { elevenLoading = false; elevenLoaded = true; if (["elevenlabs", "eleven_realtime"].includes(effectiveTtsEngine()) && $("#voices")) drawVoices(); }
  };

  const loadPluginVoices = async (provider) => {
    const entry = pluginVoices.get(provider);
    if (entry?.loaded || entry?.loading) return;
    pluginVoices.set(provider, { loaded: false, loading: true, voices: [], error: "" });
    try {
      const r = await api("GET", "/api/tts/voices?provider=" + encodeURIComponent(provider));
      pluginVoices.set(provider, { loaded: true, loading: false, voices: r.voices || [], error: "" });
    } catch (err) {
      pluginVoices.set(provider, { loaded: true, loading: false, voices: [], error: err.message });
    }
    if (p.engine === "classic" && p.tts_engine === provider && $("#voices")) drawVoices();
  };

  const drawVoices = () => {
    const ttsEngine = effectiveTtsEngine();
    const isCustomTts = p.engine === "classic" && !["elevenlabs", "openai"].includes(ttsEngine);
    $("#p-tts-voice-box").classList.toggle("hidden", !isCustomTts);
    if (isCustomTts && $("#p-tts-voice-custom").value !== (p.tts_voice || "")) {
      $("#p-tts-voice-custom").value = p.tts_voice || "";
    }

    if (ttsEngine === "elevenlabs" || ttsEngine === "eleven_realtime") {
      $("#voices").innerHTML = elevenVoiceError ? `<p class="err">${esc(elevenVoiceError)}</p>` : !elevenLoaded ? `<p class="note">Загружаю голоса вашего аккаунта ElevenLabs…</p>` : !elevenVoicesList.length ? `<p class="note">В аккаунте нет доступных голосов.</p>` :
        `<div class="voices">${elevenVoicesList.map((v) => `<div class="voice${v.voice_id === p.tts_voice ? " on" : ""}" data-v="${esc(v.voice_id)}"><div class="vn"><b>${esc(v.name)}</b><small>${esc([v.category, v.labels?.language].filter(Boolean).join(" · "))}</small></div><button class="ib" type="button" data-play="${esc(v.voice_id)}" title="Послушать на колонке" aria-label="Послушать голос">${icon("play", "solid")}</button></div>`).join("")}</div>`;
      return;
    }

    if (isCustomTts) {
      const state = pluginVoices.get(ttsEngine);
      if (!state || !state.loaded) {
        $("#voices").innerHTML = `<p class="note">Загружаю голоса плагина…</p>`;
        return;
      }
      if (state.error) {
        $("#voices").innerHTML = `<p class="err">${esc(state.error)}</p>`;
        return;
      }
      if (!state.voices.length) {
        $("#voices").innerHTML = `<p class="note">Плагин не передал список голосов. Введите ID голоса ниже или оставьте пустым.</p>`;
        return;
      }
      $("#voices").innerHTML = `<div class="voices">${state.voices.map((v) => {
        const vid = v.id || v.voice_id || v.ID || "";
        const vname = v.name || vid;
        const vnote = v.note || v.Note || "";
        return `<div class="voice${vid === p.tts_voice ? " on" : ""}" data-v="${esc(vid)}"><div class="vn"><b>${esc(vname)}</b><small>${esc(vnote)}</small></div><button class="ib" type="button" data-play="${esc(vid)}" title="Послушать на колонке" aria-label="Послушать голос">${icon("play", "solid")}</button></div>`;
      }).join("")}</div>`;
      return;
    }

    const gem = ttsEngine !== "openai";
    const groups = [...(({ gemini: S.gemini_voice_groups, chatgpt: S.chatgpt_voice_groups, grok: S.grok_voice_groups })[ttsEngine] || S.voice_groups)];
    if (!gem && p.engine === "openai" && S.live_voices.length) groups.unshift({ name: "Мои голоса", voices: S.live_voices.map((v) => ({ ID: v.id, Note: v.prompt, mine: true, name: v.name })) });
    $("#voices").innerHTML = groups.map((g) => `<div class="vgroup"><div class="gh">${esc(g.name)}</div><div class="voices">${g.voices.map((v) => `
      <div class="voice${v.ID === p.voice ? " on" : ""}" data-v="${esc(v.ID)}"><div class="vn"><b>${esc(v.name || v.ID)}</b><small>${esc(v.Note)}</small></div>
      <button class="ib" type="button" data-play="${esc(v.ID)}" title="Послушать на колонке" aria-label="Послушать на колонке">${icon("play", "solid")}</button>${v.mine ? `<button class="ib" type="button" data-delv="${esc(v.ID)}" title="Удалить голос" aria-label="Удалить голос">${icon("x")}</button>` : ""}</div>`).join("")}</div></div>`).join("");
  };

  const drawPipelineFields = () => {
    const isClassic = p.engine === "classic";
    const stt = p.stt_engine;
    $("#p-stt-model-el").classList.toggle("hidden", stt !== "elevenlabs");
    $("#p-stt-model-oa").classList.toggle("hidden", stt !== "openai");
    $("#p-stt-model-custom").classList.toggle("hidden", ["elevenlabs", "openai"].includes(stt));

    const llm = p.llm_engine;
    const usesOpenAiBox = (p.engine === "openai" || p.engine === "chatgpt") || (isClassic && ["", "openai", "chatgpt"].includes(llm));
    $("#p-openai-model-box").classList.toggle("hidden", !usesOpenAiBox);
    const customLlm = isClassic && !["", "openai", "chatgpt"].includes(llm);
    $("#p-llm-custom-box").classList.toggle("hidden", !customLlm);
    if (customLlm) {
      $("#p-llm-model-custom").placeholder = llm === "gemini" ? "gemini-3.8-flash" : llm === "grok" ? "grok-4-1-fast-non-reasoning" : "deepseek/deepseek-chat-v3-0324";
    }

    const tts = p.tts_engine;
    $("#p-tts-model-el").classList.toggle("hidden", tts !== "elevenlabs");
    $("#p-tts-model-oa").classList.toggle("hidden", tts !== "openai");
    $("#p-tts-model-custom").classList.toggle("hidden", ["elevenlabs", "openai"].includes(tts));
  };

  // движок: свои модели и свои голоса
  const drawEngine = () => {
    const options = $$("#p-engine .opt");
    options.forEach((b) => {
      const enabled = b.dataset.engine === "classic" ? classicEnabled() : voiceEnabled(b.dataset.engine);
      b.classList.toggle("hidden", !enabled);
      b.classList.toggle("on", enabled && b.dataset.engine === p.engine);
    });
    const hasOptions = options.some((b) => !b.classList.contains("hidden"));
    $("#p-engine").classList.toggle("hidden", !hasOptions);
    $("#p-no-engine").classList.toggle("hidden", hasOptions);
    const ready = p.engine === "classic" ? classicEnabled() : voiceEnabled(p.engine);
    $$(".eng").forEach((el) => el.classList.toggle("hidden", !ready || !el.dataset.for.split(" ").includes(p.engine)));
    drawPipelineFields();
    if (!ready) {
      $("#p-openai-model-box").classList.add("hidden");
      $("#p-el-voices-bar").classList.add("hidden");
      $("#p-tts-voice-box").classList.add("hidden");
      $("#v-custom").classList.add("hidden");
      $("#voices").innerHTML = `<p class="note">${hasOptions ? "Выберите движок в разделе «Мозг»." : "Голос появится после настройки плагина."}</p>`;
      return;
    }

    const ttsEngine = effectiveTtsEngine();
    $("#p-el-voices-bar").classList.toggle("hidden", !["elevenlabs", "eleven_realtime"].includes(ttsEngine));
    $("#v-custom").classList.toggle("hidden", p.engine !== "openai");

    if (ttsEngine === "elevenlabs" || ttsEngine === "eleven_realtime") {
      drawVoices();
      loadElevenVoices();
      return;
    }
    if (p.engine === "classic" && ttsEngine !== "openai") {
      drawVoices();
      loadPluginVoices(ttsEngine);
      return;
    }
    const known = (({ gemini: S.gemini_voice_groups, chatgpt: S.chatgpt_voice_groups, grok: S.grok_voice_groups })[ttsEngine] || S.voice_groups).flatMap((g) => g.voices.map((v) => v.ID.toLowerCase()));
    const mine = p.engine === "openai" && S.live_voices.some((v) => v.id === p.voice);
    if (!known.includes(String(p.voice).toLowerCase()) && !mine) {
      p.voice = ({ gemini: "Charon", chatgpt: "cove", grok: "rex" })[ttsEngine] || (p.engine === "classic" ? "alloy" : "cedar");
    }
    if (p.engine === "classic" && ttsEngine === "openai") p.tts_voice = p.voice;
    drawVoices();
  };
  drawEngine();

  $("#p-stt-engine").addEventListener("change", (e) => {
    p.stt_engine = e.target.value;
    drawPipelineFields();
    mark();
  });
  $("#p-llm-engine").addEventListener("change", (e) => {
    p.llm_engine = e.target.value;
    drawPipelineFields();
    mark();
  });
  $("#p-tts-engine").addEventListener("change", (e) => {
    p.tts_engine = e.target.value;
    drawEngine();
    mark();
  });
  $("#p-tts-voice-custom").addEventListener("input", (e) => {
    p.tts_voice = e.target.value.trim();
    drawVoices();
  });
  $("#p-el-refresh").addEventListener("click", () => { elevenLoaded = false; elevenVoiceError = ""; drawVoices(); loadElevenVoices(); });
  $("#p-engine").addEventListener("click", (e) => {
    const b = e.target.closest(".opt");
    if (!b || b.dataset.engine === p.engine) return;
    p.engine = b.dataset.engine;
    drawEngine();
    mark();
  });

  const playPreview = async (btn, voiceId) => {
    const n = ++previewSeq; btn.disabled = true;
    try {
      let body;
      if (p.engine === "classic") {
        body = { mode: "classic", engine: "classic", tts_engine: p.tts_engine, tts_model: currentTtsModel(), voice: voiceId };
      } else {
        const liveModel = { gemini: "#p-gmodel", eleven_realtime: "#p-eleven-rtmodel" }[p.engine];
        body = { voice: voiceId, engine: p.engine, tts_model: "", live_model: liveModel ? $(liveModel).value : "" };
      }
      const r = await api("POST", "/api/preview", body);
      if (n === previewSeq && !r.interrupted) toast("Образец прозвучал на колонке");
    } catch (err) { if (n === previewSeq) toast(err.message, true); } finally { btn.disabled = false; }
  };

  $("#p-tts-voice-test").addEventListener("click", () => {
    void playPreview($("#p-tts-voice-test"), $("#p-tts-voice-custom").value.trim());
  });

  $("#voices").addEventListener("click", async (e) => {
    const play = e.target.closest("[data-play]"), del = e.target.closest("[data-delv]"), card = e.target.closest(".voice");
    if (play) { // последнее нажатие побеждает: ответ прошлой пробы не перекрывает новую
      e.stopPropagation();
      await playPreview(play, play.dataset.play);
      return;
    }
    if (del) { e.stopPropagation(); if (!confirm("Удалить этот голос из списка?")) return; await act(del, async () => { await api("DELETE", "/api/voices?id=" + encodeURIComponent(del.dataset.delv)); await reload(); drawVoices(); }); return; }
    if (card) {
      if (p.engine === "eleven_realtime" || (p.engine === "classic" && p.tts_engine !== "openai")) p.tts_voice = card.dataset.v;
      else if (p.engine === "classic" && p.tts_engine === "openai") p.tts_voice = p.voice = card.dataset.v;
      else p.voice = card.dataset.v;
      drawVoices(); mark();
    }
  });
  $("#v-create").addEventListener("click", (e) => act(e.target, async () => {
    const r = await api("POST", "/api/voices", { name: $("#v-name").value.trim(), prompt: $("#v-prompt").value.trim() });
    await reload(); p.voice = r.id; drawVoices(); mark();
  }, "Голос создан — послушайте его кнопкой рядом"));

  const save = $("#p-save");
  save.addEventListener("click", () => act(save, async () => {
    Object.assign(p, {
      name: $("#p-name").value.trim(),
      prompt: $("#p-prompt").value,
      web_search: $("#p-web").checked,
      idle_s: +$("#p-idle").value || 8,
      volume: +$("#p-vol").value,
    });
    if (p.engine === "classic") {
      p.mode = "classic";
      p.stt_engine = $("#p-stt-engine").value;
      p.stt_model = currentSttModel();
      p.llm_engine = $("#p-llm-engine").value;
      if (BUILTIN_LLM.includes(p.llm_engine)) {
        p.llm_plugin = "";
        if (["", "openai", "chatgpt"].includes(p.llm_engine)) {
          p.llm_model = p.backend_model = $("#p-model").value;
        } else {
          p.llm_model = $("#p-llm-model-custom").value.trim();
        }
      } else {
        p.llm_plugin = p.llm_engine;
        p.llm_model = $("#p-llm-model-custom").value.trim();
      }
      p.tts_engine = $("#p-tts-engine").value;
      p.tts_model = currentTtsModel();
      p.live_model = "";
      if (p.tts_engine === "elevenlabs" && !p.tts_voice) throw new Error("Выберите голос ElevenLabs");
      if (p.tts_engine === "openai") {
        p.voice = p.tts_voice = p.voice || p.tts_voice || "alloy";
      } else if (p.tts_engine !== "elevenlabs") {
        p.tts_voice = $("#p-tts-voice-custom").value.trim();
        p.voice = p.tts_voice;
      }
    } else {
      p.mode = "live";
      p.backend_model = $("#p-model").value;
      p.llm_model = p.backend_model;
      p.llm_plugin = $("#p-llm-plugin")?.value || "";
      p.llm_engine = p.llm_plugin;
      p.live_model = p.engine === "gemini" ? $("#p-gmodel").value : "";
      if (p.engine === "eleven_realtime") {
        p.live_model = $("#p-eleven-rtmodel").value;
        if (!p.tts_voice) throw new Error("Выберите голос ElevenLabs");
      }
    }
    const savedRevision = revision;
    const r = await api("PUT", "/api/profile", p);
    await reload();
    if (!save.isConnected) return;
    if (isNew) {
      setDirty(false);
      go("#p/" + r.id);
      return;
    }
    const stored = S.profiles.find((profile) => profile.id === r.id);
    if (stored) $("#view h1").textContent = stored.name;
    if (revision === savedRevision) {
      if (stored) {
        $("#p-name").value = stored.name;
        $("#p-idle").value = stored.idle_s;
      }
      setDirty(false);
      $("#dirty").classList.add("hidden");
    }
  }, "Сохранено"));
  $("#p-activate")?.addEventListener("click", (e) => act(e.target, async () => { await api("POST", "/api/active", { id: p.id }); await reload(); route(); }, `Теперь отвечает «${p.name}»`));
  $("#p-del")?.addEventListener("click", (e) => { if (!confirm(`Удалить ассистента «${p.name}»?`)) return; act(e.target, async () => { await api("DELETE", "/api/profile?id=" + encodeURIComponent(p.id)); setDirty(false); await reload(); go("#home"); }, "Удалено"); });
}
