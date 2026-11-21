<script module>
  const elevenEngines = ["elevenlabs", "eleven_realtime"];
  const defaultVoices = { openai: "cedar", gemini: "Charon", chatgpt: "cove", grok: "rex" };
  const groupKeys = { gemini: "gemini_voice_groups", chatgpt: "chatgpt_voice_groups", grok: "grok_voice_groups" };

  function voiceGroups(state, engine) {
    return state[groupKeys[engine] || "voice_groups"] || [];
  }
</script>

<script>
  import { onMount, untrack } from "svelte";
  import { api, icon, tip, toast } from "../../js/core.js";
  import { reload, S } from "../../js/main.js";
  import styles from "./ProfileVoice.module.css";

  let { initial, onedit, previewModels } = $props();
  const controller = new AbortController();
  const { signal } = controller;
  let draft = $state({ ...untrack(() => initial) });
  let engine = $state("");
  let ready = $state(false);
  let hasOptions = $state(false);
  let customVoices = $state.raw(S.live_voices);
  let elevenVoices = $state.raw(null);
  let elevenLoading = $state(false);
  let elevenError = $state("");
  let previewVoice = $state("");
  let previewRequest;
  let name = $state("");
  let prompt = $state("");
  let savingVoice = $state(false);

  let eleven = $derived(elevenEngines.includes(engine));
  let selected = $derived(eleven ? draft.ttsVoice : draft.voice);
  let groups = $derived(voiceGroups(S, engine).map((group) => ({
    name: group.name,
    items: group.voices.map((item) => ({ id: item.ID, name: item.name || item.ID, note: item.Note })),
  })));
  let customItems = $derived(customVoices.map((item) => ({ id: item.id, name: item.name, note: item.prompt, custom: true })));

  onMount(() => () => {
    controller.abort();
    previewRequest?.abort();
  });

  export function configure(nextEngine, available, optionsExist) {
    if (engine !== nextEngine || !available) {
      previewRequest?.abort();
      previewVoice = "";
    }
    engine = nextEngine;
    ready = available;
    hasOptions = optionsExist;
    if (!available) return;
    if (elevenEngines.includes(nextEngine)) {
      if (elevenVoices === null && !elevenError) void refreshEleven();
      return;
    }
    selectDefaultVoice(false);
  }

  function selectDefaultVoice(dirty) {
    if (engine === "openai" && customVoices.some((item) => item.id === draft.voice)) return;
    const current = String(draft.voice || "").toLowerCase();
    const match = voiceGroups(S, engine).flatMap((group) => group.voices).find((item) => item.ID.toLowerCase() === current);
    choose(match?.ID || defaultVoices[engine] || "cedar", dirty);
  }

  export async function refreshEleven() {
    if (!ready || !elevenEngines.includes(engine) || elevenLoading) return;
    elevenLoading = true;
    elevenError = "";
    try {
      const data = await api("GET", "/api/elevenlabs/voices", undefined, { signal });
      elevenVoices = (data.voices || []).map((item) => ({
        id: item.voice_id,
        name: item.name,
        note: [item.category, item.labels?.language].filter(Boolean).join(" · "),
      }));
    } catch (error) {
      if (!signal.aborted) elevenError = error.message;
    } finally {
      elevenLoading = false;
    }
  }

  function choose(id, dirty = true) {
    if (elevenEngines.includes(engine)) {
      if (draft.ttsVoice === id) return;
      draft.ttsVoice = id;
      onedit({ tts_voice: id }, dirty);
    } else {
      if (draft.voice === id) return;
      draft.voice = id;
      onedit({ voice: id }, dirty);
    }
  }

  async function preview(id) {
    previewRequest?.abort();
    const request = new AbortController();
    previewRequest = request;
    previewVoice = id;
    try {
      const response = await api("POST", "/api/preview", { voice: id, engine, ...previewModels(engine) }, { signal: request.signal });
      if (!response.interrupted) toast("Образец прозвучал на колонке");
    } catch (error) {
      if (!request.signal.aborted) toast(error.message, true);
    } finally {
      if (previewRequest === request) {
        previewRequest = undefined;
        previewVoice = "";
      }
    }
  }

  async function syncCustomVoices() {
    try {
      await reload();
      if (!signal.aborted) customVoices = S.live_voices;
    } catch (error) {
      if (!signal.aborted) toast(error.message, true);
    }
  }

  async function createVoice(event) {
    event.preventDefault();
    if (savingVoice) return;
    savingVoice = true;
    const previous = draft.voice;
    const values = { name: name.trim(), prompt: prompt.trim() };
    try {
      const { id } = await api("POST", "/api/voices", values, { signal });
      customVoices = [...customVoices, { id, ...values }];
      if (engine === "openai" && draft.voice === previous) choose(id);
      toast("Голос создан — послушайте его кнопкой рядом");
      await syncCustomVoices();
    } catch (error) {
      if (!signal.aborted) toast(error.message, true);
    } finally {
      savingVoice = false;
    }
  }

  async function deleteVoice(id) {
    if (savingVoice || !confirm("Удалить этот голос из списка?")) return;
    savingVoice = true;
    try {
      await api("DELETE", "/api/voices?id=" + encodeURIComponent(id), undefined, { signal });
      customVoices = customVoices.filter((item) => item.id !== id);
      if (engine === "openai" && draft.voice === id) selectDefaultVoice(true);
      await syncCustomVoices();
    } catch (error) {
      if (!signal.aborted) toast(error.message, true);
    } finally {
      savingVoice = false;
    }
  }

  function changeVolume(event) {
    const volume = event.currentTarget.valueAsNumber;
    if (volume === draft.volume) return;
    draft.volume = volume;
    onedit({ volume });
  }
</script>

{#snippet options(items)}
  <div class="voices">
    {#each items as item (item.id)}
      <div class="voice {styles.voice}" class:on={selected === item.id} data-v={item.id}>
        <button class="vn {styles.choice}" type="button" aria-label={"Выбрать голос " + item.name}
          aria-pressed={selected === item.id} onclick={() => choose(item.id)}>
          <b>{item.name}</b><small>{item.note || ""}</small>
        </button>
        <button class="ib {styles.action}" type="button" data-play={item.id} title="Послушать на колонке"
          aria-label={"Послушать голос " + item.name} disabled={previewVoice === item.id} onclick={() => preview(item.id)}>
          {@html icon("play", "solid")}
        </button>
        {#if item.custom}
          <button class="ib {styles.action}" type="button" data-delv={item.id} title="Удалить голос"
            aria-label={"Удалить голос " + item.name} disabled={savingVoice} onclick={() => deleteVoice(item.id)}>
            {@html icon("x")}
          </button>
        {/if}
      </div>
    {/each}
  </div>
{/snippet}

<section class="sec">
  <div class="sec-h">
    <h2>Голос</h2>
    <p>Кнопка рядом с голосом — колонка скажет им пробную фразу. Первый раз это около цента, дальше фраза играет с самой колонки бесплатно</p>
  </div>
  <div class="sec-b">
    <div id="voices" class="vgroups">
      {#if !ready}
        <p class="note">{hasOptions ? "Выберите движок в разделе «Мозг»." : "Голос появится после настройки плагина."}</p>
      {:else if eleven}
        {#if elevenLoading}
          <p class="note" role="status">Загружаю голоса вашего аккаунта ElevenLabs…</p>
        {:else if elevenError}
          <p class="err" role="status">{elevenError}</p>
        {:else if elevenVoices?.length}
          {@render options(elevenVoices)}
        {:else}
          <p class="note">В аккаунте нет доступных голосов.</p>
        {/if}
      {:else}
        {#if engine === "openai" && customItems.length}
          <div class="vgroup"><div class="gh">Мои голоса</div>{@render options(customItems)}</div>
        {/if}
        {#each groups as group}
          <div class="vgroup"><div class="gh">{group.name}</div>{@render options(group.items)}</div>
        {/each}
      {/if}
    </div>
    <details id="v-custom" class:hidden={!ready || engine !== "openai"}>
      <summary>Создать свой голос по описанию</summary>
      <form class="inner" onsubmit={createVoice}>
        <div class="f">
          <label for="v-name">Название</label>
          <input type="text" id="v-name" placeholder="Например, Бархатный" bind:value={name}>
        </div>
        <div class="f">
          <label for="v-prompt">Описание {@html tip("Тембр, возраст, манера, темп, эмоции. Подражать конкретным людям и персонажам OpenAI не даёт, но описать манеру можно. Создание бесплатное.")}</label>
          <textarea id="v-prompt" placeholder="Низкий спокойный мужской голос, лет сорока, говорит неторопливо, с лёгкой иронией…" bind:value={prompt}></textarea>
        </div>
        <div><button class="btn" id="v-create" type="submit" disabled={savingVoice}>Создать голос</button></div>
      </form>
    </details>
    <div class="f">
      <label for="p-vol">Громкость голоса {@html tip("Насколько голос Джарвиса громче или тише остального звука колонки. Общая громкость колонки — на главной: голос меняется вместе с ней мягче. Голосом: «Джарвис, громче/тише».")}<b id="p-vol-v" class="val">{draft.volume}%</b></label>
      <input type="range" id="p-vol" min="10" max="100" step="5" value={draft.volume} oninput={changeVolume}>
    </div>
  </div>
</section>
