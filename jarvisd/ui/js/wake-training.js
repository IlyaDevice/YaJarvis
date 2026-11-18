import { act, api, sec, toast, upload } from "./core.js";
import { reload, route, showGate } from "./main.js";

const PROFILES = "/api/train/wake/profiles";
const ACTIVE = "/api/train/wake/active";
const POSITIVES = 10;
const NEGATIVES = 3;

function modelLabel(profile) {
  return profile.id ? profile.phrase : profile.phrase + " (по умолчанию)";
}

export async function bindWakeProfiles(select) {
  if (!select) return;
  try {
    const { profiles, selected } = await api("GET", PROFILES);
    if (!select.isConnected) return;
    select.replaceChildren(...profiles.map((profile) =>
      new Option(modelLabel(profile), profile.id, false, profile.id === selected)));
    select.disabled = false;
    select.onchange = async () => {
      select.disabled = true;
      try {
        await api("PUT", ACTIVE, { id: select.value });
        await reload();
        if (select.isConnected) route();
      } catch (error) {
        select.value = selected;
        select.disabled = false;
        toast(error.message, true);
      }
    };
  } catch (error) {
    if (select.isConnected) select.title = error.message;
  }
}

async function capture() {
  const response = await fetch("/api/train/wake/capture", {
    method: "POST", credentials: "same-origin", headers: { "X-Jarvis": "1" },
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    if (response.status === 401 || response.status === 428) showGate(data.setup);
    throw new Error(data.error || "Не удалось записать фразу");
  }
  return response.arrayBuffer();
}

function previewAudio(pcm) {
  const header = new ArrayBuffer(44);
  const wav = new DataView(header);
  const tag = (at, text) => [...text].forEach((c, i) => wav.setUint8(at + i, c.charCodeAt(0)));
  tag(0, "RIFF");
  wav.setUint32(4, 36 + pcm.byteLength, true);
  tag(8, "WAVEfmt ");
  wav.setUint32(16, 16, true);
  wav.setUint16(20, 1, true);
  wav.setUint16(22, 1, true);
  wav.setUint32(24, 16000, true);
  wav.setUint32(28, 32000, true);
  wav.setUint16(32, 2, true);
  wav.setUint16(34, 16, true);
  tag(36, "data");
  wav.setUint32(40, pcm.byteLength, true);
  const url = URL.createObjectURL(new Blob([header, pcm], { type: "audio/wav" }));
  const audio = new Audio(url);
  audio.controls = true;
  audio.preload = "metadata";
  return { audio, dispose: () => { audio.pause(); URL.revokeObjectURL(url); } };
}

export async function drawWakeTraining(panel) {
  let state;
  try { state = await api("GET", PROFILES); } catch (error) {
    if (panel.isConnected) panel.textContent = error.message;
    return;
  }
  if (!panel.isConnected) return;
  panel.innerHTML = [
    sec("Сохранённые фразы", "", '<div id="wake-saved" class="stack"></div>'),
    sec("Обучить фразу", state.training ? "Оставьте «Джарвис», чтобы дообучить его на своём голосе, или введите свою фразу. Проверьте модель перед включением." : "Загрузите модель для «Джарвиса» или другой фразы.",
      '<div class="f"><label for="wake-phrase">Слово или фраза</label><input type="text" id="wake-phrase" maxlength="48" value="Джарвис" autocomplete="off"></div>' +
      (state.training ? '<div id="wake-record"></div>' : '') +
      '<div class="f"><label for="wake-model">Загрузить готовую модель</label><input type="file" id="wake-model" accept=".bin,application/octet-stream"></div>'),
  ].join("");

  const saved = panel.querySelector("#wake-saved");
  saved.innerHTML = '<div class="f"><label for="wake-pick">Активная фраза</label><select id="wake-pick" disabled></select></div>';
  void bindWakeProfiles(saved.querySelector("select"));

  for (const profile of state.profiles.filter((profile) => profile.id)) {
    const row = document.createElement("div");
    row.className = "row";
    const label = document.createElement("span");
    label.textContent = profile.phrase;
    row.append(label);
    if (state.training) {
      const test = document.createElement("button");
      test.type = "button";
      test.className = "btn";
      test.textContent = "Проверить";
      const result = document.createElement("span");
      result.setAttribute("role", "status");
      test.onclick = () => act(test, async () => {
        result.textContent = "Приготовьтесь…";
        await new Promise((resolve) => setTimeout(resolve, 800));
        if (!panel.isConnected) return;
        const recording = capture();
        await new Promise((resolve) => setTimeout(resolve, 250));
        result.textContent = "Говорите «" + profile.phrase + "»";
        try {
          const data = await upload("POST", "/api/train/wake/test/" + profile.id, await recording);
          result.textContent = data.detected ? "Фраза распознана" : "Фраза не распознана";
        } catch (error) {
          result.textContent = error.message;
        }
      });
      row.append(test, result);
    }
    if (profile.id !== state.selected) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "btn";
      remove.textContent = "Удалить";
      remove.onclick = () => {
        if (!confirm("Удалить модель «" + profile.phrase + "»?")) return;
        return act(remove, async () => {
          await api("DELETE", PROFILES + "/" + profile.id);
          if (panel.isConnected) await drawWakeTraining(panel);
        });
      };
      row.append(remove);
    }
    saved.append(row);
  }

  const name = panel.querySelector("#wake-phrase");
  const file = panel.querySelector("#wake-model");
  file.onchange = () => {
    if (!file.files[0]) return;
    void act(file, async () => {
      try {
        await upload("PUT", PROFILES + "?phrase=" + encodeURIComponent(name.value.trim()), file.files[0]);
        if (panel.isConnected) await drawWakeTraining(panel);
      } finally {
        file.value = "";
      }
    }, "Модель сохранена. Выберите её в списке.");
  };
  const recorder = panel.querySelector("#wake-record");
  if (recorder) bindRecorder(panel, recorder, name, file);
}

function bindRecorder(panel, recorder, name, file) {
  const clips = [];
  recorder.innerHTML = '<p role="status" class="muted"></p><div class="row"><button class="btn" type="button" data-record>Записать</button>' +
    '<select data-review aria-label="Запись для прослушивания"></select><button class="btn" type="button" data-redo disabled>Перезаписать</button>' +
    '<button class="btn primary" type="button" data-train disabled>Обучить</button></div><div data-preview></div>';
  const status = recorder.querySelector("[role=status]");
  const record = recorder.querySelector("[data-record]");
  const redo = recorder.querySelector("[data-redo]");
  const train = recorder.querySelector("[data-train]");
  const previewDiv = recorder.querySelector("[data-preview]");
  const review = recorder.querySelector("[data-review]");
  let preview;
  const resetPreview = () => {
    preview?.dispose();
    preview = null;
    previewDiv.replaceChildren();
  };
  window.addEventListener("hashchange", resetPreview, { once: true });

  function listen() {
    resetPreview();
    const pcm = clips[Number(review.value)];
    if (!pcm) return;
    preview = previewAudio(pcm);
    previewDiv.append(preview.audio);
  }
  review.onchange = listen;

  function show() {
    const i = clips.length;
    if (i === POSITIVES + NEGATIVES) status.textContent = "Записи готовы. Нажмите «Обучить».";
    else if (i >= POSITIVES) status.textContent = "Фон " + (i - POSITIVES + 1) + " из " + NEGATIVES + ". Скажите обычную фразу без слова активации.";
    else status.textContent = "Запись " + (i + 1) + " из " + POSITIVES + ". Когда появится «Говорите», произнесите фразу один раз.";
    name.readOnly = i > 0;
    file.disabled = i > 0;
    record.disabled = i === POSITIVES + NEGATIVES;
    redo.disabled = review.disabled = !i;
    train.disabled = i !== POSITIVES + NEGATIVES;
  }

  async function recordClip(index) {
    if (!name.value.trim()) { toast("Введите слово или фразу", true); name.focus(); return; }
    record.disabled = redo.disabled = review.disabled = train.disabled = file.disabled = name.readOnly = true;
    resetPreview();
    status.textContent = "Приготовьтесь…";
    try {
      await new Promise((resolve) => setTimeout(resolve, 700));
      if (!panel.isConnected) return;
      const recording = capture();
      await new Promise((resolve) => setTimeout(resolve, 250));
      if (!panel.isConnected) { await recording; return; }
      status.textContent = index < POSITIVES ? "Говорите «" + name.value.trim() + "»" : "Говорите обычную фразу";
      const pcm = await recording;
      if (!panel.isConnected) return;
      const newClip = index === clips.length;
      clips[index] = pcm;
      if (newClip) review.add(new Option(index < POSITIVES ? "Фраза " + (index + 1) : "Фон " + (index - POSITIVES + 1), index));
      review.value = index;
      listen();
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (panel.isConnected) show();
    }
  }

  record.onclick = () => recordClip(clips.length);
  redo.onclick = () => recordClip(Number(review.value));
  train.onclick = async () => {
    record.disabled = redo.disabled = train.disabled = true;
    status.textContent = "Обучаю и проверяю модель…";
    try {
      await upload("POST", "/api/train/wake/train?phrase=" + encodeURIComponent(name.value.trim()), new Blob(clips));
      if (panel.isConnected) {
        resetPreview();
        await drawWakeTraining(panel);
        toast("Модель сохранена. Выберите её в списке.");
      }
    } catch (error) {
      toast(error.message, true);
      if (panel.isConnected) show();
    }
  };
  show();
}
