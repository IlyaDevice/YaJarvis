import { mount, unmount } from "svelte";
import { $, $$, act, api, esc, sec, setDirty, tip } from "../js/core.js";
import { S, go, reload, route, voiceEnabled } from "../js/main.js";
import ProfileBasics from "./ProfileBasics/ProfileBasics.svelte";
import ProfileLighting from "./ProfileLighting/ProfileLighting.svelte";
import ProfileVoice from "./ProfileVoice/ProfileVoice.svelte";

// ---------------------------------------------------------------- ассистент
export function mountProfile(target, id) {
  const isNew = id === "new";
  const base = S.profiles.find((x) => x.id === id);
  if (!isNew && !base) { go("#home"); return {}; }
  const p = structuredClone(base || { id: "", name: "", color: (S.profiles.length % 6) + 1, mode: "live", engine: "", voice: "", effort: "low", priority: true, web_search: true, idle_s: 8, prompt: S.prompts[0]?.id || "" });
  p.led ||= { ...(S.settings.led || {}) }; p.led.brightness ||= 45;
  p.volume ||= S.settings.volume || 40; p.city ??= S.settings.city || ""; p.timezone ||= S.settings.timezone || "UTC";
  if (p.engine === "live") p.engine = "openai";
  if(p.mode==="classic")p.engine="elevenlabs";
  p.llm_model ||= p.backend_model;
  p.tts_model ||= "eleven_flash_v2_5";
  if(p.engine==="elevenlabs")p.backend_model=p.llm_model;
  if (!S.prompts.some((prompt) => prompt.id === p.prompt)) p.prompt = S.prompts[0]?.id || "";
  const active = p.id === S.active;
  const textPlugins = [...(S.text_plugins || [])];
  if (p.llm_plugin && !textPlugins.some((plugin) => plugin.id === p.llm_plugin)) {
    textPlugins.push({ id: p.llm_plugin, name: p.llm_plugin + " (отключён)" });
  }
  target.innerHTML = `<div id="profile-basics"></div>
  ${sec("Мозг", "Выберите живой разговор или классическую цепочку: распознавание речи → текстовая модель → озвучка",
    `<p class="note hidden" id="p-no-engine">Установите и включите голосовой плагин в <a href="#connections/plugins">«Подключениях»</a>.</p>
    <div class="choice" id="p-engine">
      <button class="opt" data-engine="elevenlabs" type="button"><b>ElevenLabs — классический</b><small>Распознавание Scribe и голос ElevenLabs. Текстовая модель OpenAI думает и вызывает инструменты. Нужны ключи ElevenLabs и OpenAI</small></button>
      <button class="opt" data-engine="eleven_realtime" type="button"><b>ElevenLabs — живой разговор</b><small>Eleven Realtime Mini или Max: распознавание, мозг, голос и перебивания вместе. Нужен только ключ ElevenLabs</small></button>
      <button class="opt" data-engine="chatgpt" type="button"><b>GPT-Live по подписке</b><small>Ваш ChatGPT Plus или Pro: минуты не оплачиваются, есть лимиты подписки. Нужен вход в «Подключениях»</small></button>
      <button class="opt" data-engine="openai" type="button"><b>GPT-Live</b><small>OpenAI по ключу API. Около 5 центов за минуту разговора</small></button>
      <button class="opt" data-engine="gemini" type="button"><b>Gemini Live</b><small>Google. Примерно вдвое дешевле, есть бесплатный уровень. Нужен ключ Gemini в «Подключениях»</small></button>
      <button class="opt" data-engine="grok" type="button"><b>Grok Voice</b><small>xAI. Около 5 центов за минуту, сам ищет в интернете. Нужен ключ xAI в «Подключениях»</small></button>
    </div>
      <div class="eng" data-for="elevenlabs"><div class="f half"><label>Распознавание речи</label><select id="p-stt-model">${["scribe_v3_live","scribe_v2_realtime"].map(m=>`<option value="${m}"${m===(p.stt_model || "scribe_v2_realtime")?" selected":""}>${m==="scribe_v3_live"?"Scribe v3 Live — определяет конец мысли":"Scribe v2 Realtime"}</option>`).join("")}</select></div><div class="f half"><label>Модель озвучки</label><select id="p-tts-model">${["eleven_v4_turbo","eleven_v4","eleven_v3_conversational","eleven_v3","eleven_flash_v2_5","eleven_multilingual_v2"].map(m=>`<option value="${m}"${m===p.tts_model?" selected":""}>${m}</option>`).join("")}</select></div></div>
      <div class="eng" data-for="eleven_realtime"><div class="f half"><label>Разговорная модель</label><select id="p-eleven-rtmodel">${["eleven_realtime_v1_mini","eleven_realtime_v1_max"].map(m=>`<option value="${m}"${m===p.live_model?" selected":""}>${m.endsWith("max")?"Realtime v1 Max — сложные задачи и инструменты":"Realtime v1 Mini — быстрый разговор"}</option>`).join("")}</select></div></div>
      <div class="eng" data-for="elevenlabs eleven_realtime"><p class="note">Голос выбирается ниже из вашего аккаунта ElevenLabs.</p><button class="btn" id="p-el-refresh" type="button">Обновить голоса ElevenLabs</button></div>
      <div class="eng" data-for="gemini"><div class="f half"><label>Модель ${tip("С бесплатным ключом из AI Studio все эти модели работают без оплаты, но у каждой свой дневной лимит. Когда он кончается, Джарвис сам переходит на следующую модель в списке.")}</label><select id="p-gmodel">${(S.gemini_model_info?.length ? S.gemini_model_info : S.gemini_models.map((id) => ({ id, name: id, note: "" }))).map((m) => `<option value="${esc(m.id)}"${m.id === p.live_model ? " selected" : ""}>${esc(m.name)}${m.note ? ` — ${esc(m.note)}` : ""}</option>`).join("")}</select></div></div>
    <div class="eng" data-for="openai chatgpt elevenlabs"><div class="f half"><label>Текстовая модель ${tip("Чем умнее модель, тем дольше она думает. Для голосового помощника обычно хватает самой быстрой.")}</label><select id="p-model">${S.backend_models.map((m) => `<option${m === p.backend_model ? " selected" : ""}>${esc(m)}</option>`).join("")}</select></div>
      <div class="f"><label>Сколько думать ${tip("Доступные уровни зависят от выбранной текстовой модели: не все модели поддерживают «не думать». Немного — быстрее, средне и долго — для сложных вопросов.")}</label><div class="seg" id="p-effort">${S.efforts.map((e) => `<button type="button" data-v="${e}" class="${e === p.effort ? "on" : ""}">${({ none: "не думать", low: "немного", medium: "средне", high: "долго" })[e] || e}</button>`).join("")}</div></div>
    <label class="sw"><input type="checkbox" id="p-priority"${p.priority ? " checked" : ""}><span class="tr"></span>Приоритет ${tip("Запросы обрабатываются в быстрой очереди OpenAI: ответ быстрее, но стоит дороже.")}</label></div>
    ${textPlugins.length ? `<div class="f"><label for="p-llm-plugin">Текстовая модель из плагина</label>
      <select id="p-llm-plugin"><option value="">По настройкам ассистента</option>${textPlugins.map((plugin) => `<option value="${esc(plugin.id)}"${p.llm_plugin === plugin.id ? " selected" : ""}>${esc(plugin.name)}</option>`).join("")}</select>
      <small>Используется в переписке и классическом голосовом режиме.</small></div>` : ""}
    <div class="stack">
      <label class="sw"><input type="checkbox" id="p-web"${p.web_search ? " checked" : ""}><span class="tr"></span>Поиск в интернете ${tip("Для погоды, новостей, курсов и всего свежего. Без него Джарвис знает только то, чему его научили.")}</label></div>
    <div class="f narrow"><label>Конец разговора, с ${tip("Если вы оба молчите столько секунд, разговор закрывается и колонка снова ждёт «Джарвис». Пока разговор открыт, звать по имени не нужно.")}</label><input type="number" id="p-idle" min="2" max="60" step="1" value="${p.idle_s}"></div>`)}
  <div id="profile-voice"></div>
  <div id="profile-lighting"></div>
  <div class="savebar"><button class="btn primary" id="p-save">${isNew ? "Создать" : "Сохранить"}</button>
    ${!isNew && !active ? `<button class="btn" id="p-activate" data-tip="Этот ассистент будет отвечать на «Джарвис»">Сделать активным</button>` : ""}
    ${!isNew && !active ? `<button class="btn ghost danger" id="p-del">Удалить</button>` : ""}<span class="dirty hidden" id="dirty">есть несохранённые изменения</span></div>`;

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
      initial: { voice: p.voice, ttsVoice: p.tts_voice, volume: p.volume },
      onedit: (change, dirty = true) => {
        Object.assign(p, change);
        if (dirty) mark();
      },
      previewModels: (engine) => {
        const liveModel = { gemini: "#p-gmodel", eleven_realtime: "#p-eleven-rtmodel" }[engine];
        return {
          tts_model: engine === "elevenlabs" ? $("#p-tts-model", target).value : "",
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

  const drawEngine = () => {
    const options = $$("#p-engine .opt");
    options.forEach((b) => {
      const enabled = voiceEnabled(b.dataset.engine);
      b.classList.toggle("hidden", !enabled);
      b.classList.toggle("on", enabled && b.dataset.engine === p.engine);
    });
    const hasOptions = options.some((b) => !b.classList.contains("hidden"));
    $("#p-engine").classList.toggle("hidden", !hasOptions);
    $("#p-no-engine").classList.toggle("hidden", hasOptions);
    const ready = voiceEnabled(p.engine);
    $$(".eng").forEach((el) => el.classList.toggle("hidden", !ready || !el.dataset.for.split(" ").includes(p.engine)));
    voice.configure(p.engine, ready, hasOptions);
  };
  drawEngine();
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
    const payload = { ...p, name: p.name.trim(), backend_model: $("#p-model").value,
      priority: $("#p-priority").checked, web_search: $("#p-web").checked, idle_s: +$("#p-idle").value || 8, mode: "live",
      llm_plugin: $("#p-llm-plugin")?.value || "",
      live_model: p.engine === "gemini" ? $("#p-gmodel").value : "" };
    payload.mode=p.engine==="elevenlabs"?"classic":"live";
    if(p.engine==="eleven_realtime"){payload.live_model=$("#p-eleven-rtmodel").value;if(!payload.tts_voice)throw new Error("Выберите голос ElevenLabs");}
    if(p.engine==="elevenlabs"){payload.llm_model=payload.backend_model;payload.tts_model=$("#p-tts-model").value;payload.stt_engine="elevenlabs";payload.stt_model=$("#p-stt-model").value;if(!payload.tts_voice)throw new Error("Выберите голос ElevenLabs");}
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
