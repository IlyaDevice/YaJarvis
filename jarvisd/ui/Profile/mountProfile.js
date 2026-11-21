import { mount, unmount } from "svelte";
import { $, $$, act, api, esc, sec, setDirty, tip } from "../js/core.js";
import { S, classicEnabled, go, isClassicProfile, reload, route, sttReady, ttsReady, voiceEnabled } from "../js/main.js";
import ProfileBasics from "./ProfileBasics/ProfileBasics.svelte";
import ProfileLighting from "./ProfileLighting/ProfileLighting.svelte";
import ProfileVoice from "./ProfileVoice/ProfileVoice.svelte";

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

export function mountProfile(target, id) {
  const isNew = id === "new";
  const base = S.profiles.find((x) => x.id === id);
  if (!isNew && !base) { go("#home"); return {}; }
  const p = structuredClone(base || { id: "", name: "", color: (S.profiles.length % 6) + 1, mode: "live", engine: "", voice: "", effort: "low", priority: true, web_search: true, idle_s: 8, prompt: S.prompts[0]?.id || "" });
  p.led ||= { ...(S.settings.led || {}) }; p.led.brightness ||= 45;
  p.volume ||= S.settings.volume || 40; p.city ??= S.settings.city || ""; p.timezone ||= S.settings.timezone || "UTC";
  if (p.engine === "live") p.engine = "openai";
  const classicInitial = isClassicProfile(p);
  if (classicInitial) {
    p.tts_engine ||= p.engine === "openai" ? "openai" : "elevenlabs";
    if (p.tts_engine === "openai") p.tts_voice ||= p.voice || "alloy";
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

  if (!S.prompts.some((prompt) => prompt.id === p.prompt)) p.prompt = S.prompts[0]?.id || "";
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

  target.innerHTML = `<div id="profile-basics"></div>
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
  <div id="profile-voice"></div>
  <div id="profile-lighting"></div>
  <div class="savebar"><button class="btn primary" id="p-save">${isNew ? "Создать" : "Сохранить"}</button>
    ${!isNew && !active ? `<button class="btn" id="p-activate" data-tip="Этот ассистент будет отвечать на «Джарвис»">Сделать активным</button>` : ""}
    ${!isNew && !active ? `<button class="btn ghost danger" id="p-del">Удалить</button>` : ""}<span class="dirty hidden" id="dirty">есть несохранённые изменения</span></div>`;

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

  const controller = new AbortController();
  const { signal } = controller;
  let revision = 0;
  const mark = () => { revision++; setDirty(true); $("#dirty").classList.remove("hidden"); };
  const basics = mount(ProfileBasics, {
    target: $("#profile-basics", target),
    props: {
      initial: { name: p.name, color: p.color, prompt: p.prompt }, prompts: S.prompts, isNew, active,
      onedit: (change) => { Object.assign(p, change); mark(); },
    },
  });
  const lighting = mount(ProfileLighting, {
    target: $("#profile-lighting", target),
    props: {
      initial: p.led, previewAvailable: Boolean(S.speaker.leds), globallyEnabled: S.settings.led?.enabled !== false,
      onedit: (change) => { Object.assign(p.led, change); mark(); },
    },
  });
  const voice = mount(ProfileVoice, {
    target: $("#profile-voice", target),
    props: {
      initial: { voice: p.voice, ttsVoice: p.tts_voice, ttsProvider: p.engine === "eleven_realtime" ? "elevenlabs" : p.tts_engine, volume: p.volume },
      onedit: (change, dirty = true) => {
        Object.assign(p, change);
        if (dirty) mark();
      },
      previewModels: (engine) => {
        if (engine === "classic") return { mode: "classic", tts_engine: p.tts_engine, tts_model: currentTtsModel() };
        const liveModel = { gemini: "#p-gmodel", eleven_realtime: "#p-eleven-rtmodel" }[engine];
        return {
          tts_model: "",
          live_model: liveModel ? $(liveModel, target).value : "",
        };
      },
    },
  });
  const markLegacy = (event) => {
    if (!event.target.closest("#profile-basics, #profile-lighting, #profile-voice")) mark();
  };
  target.addEventListener("input", markLegacy, { signal });
  target.addEventListener("change", markLegacy, { signal });
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
  $("#p-effort").addEventListener("click", (e) => { const b = e.target.closest("button"); if (!b) return; p.effort = b.dataset.v; $$("#p-effort button").forEach((x) => x.classList.toggle("on", x === b)); mark(); });

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
    const classic = p.engine === "classic";
    const ready = classic ? classicEnabled() : voiceEnabled(p.engine);
    const ttsProvider = classic ? p.tts_engine : p.engine === "eleven_realtime" ? "elevenlabs" : p.engine;
    $$(".eng").forEach((el) => el.classList.toggle("hidden", !ready || !el.dataset.for.split(" ").includes(p.engine)));
    drawPipelineFields();
    if (!ready) $("#p-openai-model-box").classList.add("hidden");
    $("#p-el-voices-bar").classList.toggle("hidden", !ready || ttsProvider !== "elevenlabs");
    voice.configure(p.engine, ttsProvider, ready, hasOptions);
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
  $("#p-el-refresh").addEventListener("click", () => void voice.refreshEleven());
  $("#p-engine").addEventListener("click", (e) => {
    const button = e.target.closest(".opt");
    if (!button || button.dataset.engine === p.engine) return;
    p.engine = button.dataset.engine;
    drawEngine();
    mark();
  });

  const save = $("#p-save");
  save.addEventListener("click", () => act(save, async () => {
    const payload = {
      ...p,
      name: p.name.trim(),
      priority: $("#p-priority").checked,
      web_search: $("#p-web").checked,
      idle_s: +$("#p-idle").value || 8,
    };
    if (p.engine === "classic") {
      payload.mode = "classic";
      payload.stt_model = currentSttModel();
      payload.llm_plugin = BUILTIN_LLM.includes(p.llm_engine) ? "" : p.llm_engine;
      if (["", "openai", "chatgpt"].includes(p.llm_engine)) {
        payload.llm_model = payload.backend_model = $("#p-model").value;
      } else {
        payload.llm_model = $("#p-llm-model-custom").value.trim();
      }
      payload.tts_model = currentTtsModel();
      payload.live_model = "";
      if (p.tts_engine === "openai") payload.voice = payload.tts_voice = p.tts_voice || "alloy";
      else if (p.tts_engine !== "elevenlabs") payload.voice = p.tts_voice;
    } else {
      payload.mode = "live";
      payload.llm_model = payload.backend_model = $("#p-model").value;
      payload.llm_plugin = $("#p-llm-plugin")?.value || "";
      payload.llm_engine = payload.llm_plugin;
      payload.live_model = p.engine === "gemini" ? $("#p-gmodel").value : "";
      if (p.engine === "eleven_realtime") {
        payload.live_model = $("#p-eleven-rtmodel").value;
      }
    }
    if ((p.engine === "eleven_realtime" || (p.engine === "classic" && p.tts_engine === "elevenlabs")) && !payload.tts_voice) {
      throw new Error("Выберите голос ElevenLabs");
    }
    const savedRevision = revision;
    const r = await api("PUT", "/api/profile", payload);
    await reload();
    if (!save.isConnected) return;
    if (isNew) {
      setDirty(false);
      go("#p/" + r.id);
      return;
    }
    const stored = S.profiles.find((profile) => profile.id === r.id);
    const unchanged = revision === savedRevision;
    if (stored) basics.updateSavedName(stored.name, unchanged);
    if (unchanged) {
      if (stored) {
        p.name = stored.name;
        $("#p-idle").value = stored.idle_s;
      }
      setDirty(false);
      $("#dirty").classList.add("hidden");
    }
  }, "Сохранено"));
  $("#p-activate")?.addEventListener("click", (e) => act(e.target, async () => { await api("POST", "/api/active", { id: p.id }); await reload(); route(); }, `Теперь отвечает «${p.name}»`));
  $("#p-del")?.addEventListener("click", (e) => { if (!confirm(`Удалить ассистента «${p.name}»?`)) return; act(e.target, async () => { await api("DELETE", "/api/profile?id=" + encodeURIComponent(p.id)); setDirty(false); await reload(); go("#home"); }, "Удалено"); });
  return {
    dispose() {
      controller.abort();
      return Promise.all([unmount(basics), unmount(voice), unmount(lighting)]);
    },
  };
}
