const $ = (id) => document.getElementById(id);

const CONFIG_STORAGE_KEY = "say-lab-config-v2";
const USAGE_STORAGE_KEY = "say-lab-usage-v1";

const DEFAULT_CONFIG = {
  llm: {
    base_url: "https://api.deepseek.com/v1",
    endpoint: "",
    model: "deepseek-chat",
    api_key: "",
    timeout: 90,
  },
  tts: {
    default_provider: "auto",
    provider: "auto",
    auto_order: ["google_chirp", "google_wavenet", "custom"],
    monthly_limits: {
      google_chirp: 800000,
      google_wavenet: 4000000,
      custom: 800000,
    },
    google: {
      project_id: "",
      client_email: "",
      private_key: "",
      private_key_id: "",
      token_url: "https://oauth2.googleapis.com/token",
      tts_url: "https://texttospeech.googleapis.com/v1/text:synthesize",
      timeout: 60,
    },
    custom: {
      base_url: "https://api.openai.com/v1",
      api_key: "",
      model: "gpt-4o-mini-tts",
      voice: "alloy",
      response_format: "mp3",
      speed: 1,
      timeout: 60,
    },
  },
};

const state = {
  config: loadLocalConfig(),
  usage: loadUsage(),
  scriptLines: [],
  translationLines: [],
  subtitleCueStops: [],
  activeLine: -1,
  audioUrl: "",
  audioAbortController: null,
  playbackState: "idle",
  playbackSource: "",
  translationLanguage: localStorage.getItem("say-lab-translation-language") || "",
};

const providerLabels = {
  google_chirp: "Google Chirp 3 HD",
  google_wavenet: "Google WaveNet",
  custom: "OpenAI-compatible TTS",
};

$("clear-btn").addEventListener("click", () => {
  $("query").value = "";
  $("query").focus();
});

$("rate").addEventListener("input", () => {
  $("rate-value").textContent = Number($("rate").value).toFixed(2);
});

$("play-input").addEventListener("click", () => {
  if (state.playbackSource === "input" && state.playbackState !== "idle") {
    stopAudio();
    return;
  }
  playInput();
});
$("analyze-btn").addEventListener("click", analyze);
$("play-script").addEventListener("click", toggleScriptPlayback);
$("pause-script").addEventListener("click", togglePause);
$("config-toggle").addEventListener("click", toggleConfigPanel);
$("config-reload").addEventListener("click", loadConfigForm);
$("config-form").addEventListener("submit", saveConfigForm);
$("translation-language").value = state.translationLanguage;
$("translation-language").addEventListener("change", () => {
  state.translationLanguage = $("translation-language").value;
  localStorage.setItem("say-lab-translation-language", state.translationLanguage);
});
$("copy-script").addEventListener("click", async () => {
  await navigator.clipboard.writeText(scriptClipboardText());
  notice("跟读稿已复制。");
});
$("audio").addEventListener("timeupdate", syncSubtitle);
$("audio").addEventListener("ended", handleAudioEnded);

async function init() {
  renderStatus();
  renderSubtitles();
  updateAudioControls();
}

async function toggleConfigPanel() {
  const panel = $("config-panel");
  const willOpen = panel.hidden;
  panel.hidden = !willOpen;
  $("config-toggle").textContent = willOpen ? "收起配置" : "配置";
  if (willOpen) loadConfigForm();
}

function loadConfigForm() {
  state.config = loadLocalConfig();
  fillConfigForm(state.config);
  setConfigStatus("");
}

function saveConfigForm(event) {
  event.preventDefault();
  state.config = collectConfigForm();
  saveLocalConfig(state.config);
  fillConfigForm(state.config);
  renderStatus();
  setConfigStatus("配置已保存在此浏览器。");
}

function fillConfigForm(config) {
  const cfg = normalizeClientConfig(config || {});
  $("config-source").textContent = "配置只保存在这个浏览器；Demo 服务器不会保存 API Key。";

  setField("cfg-llm-base-url", cfg.llm.base_url);
  setField("cfg-llm-endpoint", cfg.llm.endpoint);
  setField("cfg-llm-model", cfg.llm.model);
  setField("cfg-llm-api-key", cfg.llm.api_key);
  setField("cfg-llm-timeout", cfg.llm.timeout);

  setField("cfg-tts-default-provider", cfg.tts.default_provider);
  setField("cfg-tts-auto-order", (cfg.tts.auto_order || []).join(", "));
  setField("cfg-limit-google-chirp", cfg.tts.monthly_limits.google_chirp);
  setField("cfg-limit-google-wavenet", cfg.tts.monthly_limits.google_wavenet);
  setField("cfg-limit-custom", cfg.tts.monthly_limits.custom);

  setField("cfg-google-project-id", cfg.tts.google.project_id);
  setField("cfg-google-client-email", cfg.tts.google.client_email);
  setField("cfg-google-private-key-id", cfg.tts.google.private_key_id);
  setField("cfg-google-private-key", cfg.tts.google.private_key);
  setField("cfg-google-token-url", cfg.tts.google.token_url);
  setField("cfg-google-tts-url", cfg.tts.google.tts_url);
  setField("cfg-google-timeout", cfg.tts.google.timeout);

  setField("cfg-custom-base-url", cfg.tts.custom.base_url);
  setField("cfg-custom-api-key", cfg.tts.custom.api_key);
  setField("cfg-custom-model", cfg.tts.custom.model);
  setField("cfg-custom-voice", cfg.tts.custom.voice);
  setField("cfg-custom-format", cfg.tts.custom.response_format);
  setField("cfg-custom-speed", cfg.tts.custom.speed);
  setField("cfg-custom-timeout", cfg.tts.custom.timeout);
}

function collectConfigForm() {
  const cfg = normalizeClientConfig(state.config);
  cfg.llm.base_url = fieldValue("cfg-llm-base-url");
  cfg.llm.endpoint = fieldValue("cfg-llm-endpoint");
  cfg.llm.model = fieldValue("cfg-llm-model");
  cfg.llm.api_key = fieldValue("cfg-llm-api-key");
  cfg.llm.timeout = numberFieldValue("cfg-llm-timeout", cfg.llm.timeout);

  cfg.tts.default_provider = fieldValue("cfg-tts-default-provider") || "auto";
  cfg.tts.provider = cfg.tts.default_provider;
  cfg.tts.auto_order = splitCSV($("cfg-tts-auto-order").value);
  cfg.tts.monthly_limits.google_chirp = numberFieldValue("cfg-limit-google-chirp", 0);
  cfg.tts.monthly_limits.google_wavenet = numberFieldValue("cfg-limit-google-wavenet", 0);
  cfg.tts.monthly_limits.custom = numberFieldValue("cfg-limit-custom", 0);

  cfg.tts.google.project_id = fieldValue("cfg-google-project-id");
  cfg.tts.google.client_email = fieldValue("cfg-google-client-email");
  cfg.tts.google.private_key_id = fieldValue("cfg-google-private-key-id");
  cfg.tts.google.private_key = fieldValue("cfg-google-private-key");
  cfg.tts.google.token_url = fieldValue("cfg-google-token-url");
  cfg.tts.google.tts_url = fieldValue("cfg-google-tts-url");
  cfg.tts.google.timeout = numberFieldValue("cfg-google-timeout", cfg.tts.google.timeout);

  cfg.tts.custom.base_url = fieldValue("cfg-custom-base-url");
  cfg.tts.custom.api_key = fieldValue("cfg-custom-api-key");
  cfg.tts.custom.model = fieldValue("cfg-custom-model");
  cfg.tts.custom.voice = fieldValue("cfg-custom-voice");
  cfg.tts.custom.response_format = fieldValue("cfg-custom-format");
  cfg.tts.custom.speed = numberFieldValue("cfg-custom-speed", cfg.tts.custom.speed);
  cfg.tts.custom.timeout = numberFieldValue("cfg-custom-timeout", cfg.tts.custom.timeout);
  return cfg;
}

function loadLocalConfig() {
  try {
    const raw = localStorage.getItem(CONFIG_STORAGE_KEY);
    return normalizeClientConfig(raw ? JSON.parse(raw) : {});
  } catch {
    return normalizeClientConfig({});
  }
}

function saveLocalConfig(config) {
  localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(normalizeClientConfig(config)));
}

function loadUsage() {
  try {
    const raw = localStorage.getItem(USAGE_STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function saveUsage() {
  localStorage.setItem(USAGE_STORAGE_KEY, JSON.stringify(state.usage));
}

function normalizeClientConfig(config) {
  const input = config || {};
  const llm = input.llm || {};
  const tts = input.tts || {};
  const custom = tts.custom || {};
  const google = tts.google || {};
  const monthlyLimits = tts.monthly_limits || {};
  const autoOrder = Array.isArray(tts.auto_order) ? tts.auto_order : splitCSV(tts.auto_order);
  return {
    llm: {
      ...DEFAULT_CONFIG.llm,
      ...llm,
      timeout: positiveNumber(llm.timeout, DEFAULT_CONFIG.llm.timeout),
    },
    tts: {
      default_provider: tts.default_provider || tts.provider || DEFAULT_CONFIG.tts.default_provider,
      provider: tts.provider || tts.default_provider || DEFAULT_CONFIG.tts.provider,
      auto_order: autoOrder.length ? autoOrder : [...DEFAULT_CONFIG.tts.auto_order],
      monthly_limits: {
        ...DEFAULT_CONFIG.tts.monthly_limits,
        ...monthlyLimits,
        google_chirp: nonNegativeNumber(monthlyLimits.google_chirp, DEFAULT_CONFIG.tts.monthly_limits.google_chirp),
        google_wavenet: nonNegativeNumber(monthlyLimits.google_wavenet, DEFAULT_CONFIG.tts.monthly_limits.google_wavenet),
        custom: nonNegativeNumber(monthlyLimits.custom, DEFAULT_CONFIG.tts.monthly_limits.custom),
      },
      google: {
        ...DEFAULT_CONFIG.tts.google,
        ...google,
        timeout: positiveNumber(google.timeout, DEFAULT_CONFIG.tts.google.timeout),
      },
      custom: {
        ...DEFAULT_CONFIG.tts.custom,
        ...custom,
        speed: positiveNumber(custom.speed, DEFAULT_CONFIG.tts.custom.speed),
        timeout: positiveNumber(custom.timeout, DEFAULT_CONFIG.tts.custom.timeout),
      },
    },
  };
}

function setField(id, value) {
  $(id).value = value ?? "";
}

function fieldValue(id) {
  return $(id).value.trim();
}

function numberFieldValue(id, fallback) {
  const value = $(id).value.trim();
  if (value === "") return Number(fallback || 0);
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : Number(fallback || 0);
}

function splitCSV(value) {
  return String(value || "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function positiveNumber(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function nonNegativeNumber(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}

function setConfigStatus(text) {
  $("config-status").textContent = text || "";
}

function renderStatus() {
  const cfg = state.config;
  const llmReady = isLLMConfigured(cfg.llm);
  const ttsReady = isTTSConfigured(cfg.tts);
  const ttsLine = ttsReady
    ? providerSummary(cfg.tts)
    : "待配置 TTS";
  $("status-card").innerHTML = `
    <div class="status-title">Demo 配置</div>
    <div class="status-line">
      <strong>分析</strong> ${llmReady ? "已配置" : "待配置 LLM"}<br>
      <strong>发音</strong> ${escapeHTML(ttsLine)}<br>
      <span class="status-note">API Key 只保存在此浏览器。</span>
    </div>`;
}

function isLLMConfigured(llm) {
  return Boolean(llm?.base_url && llm?.model && llm?.api_key);
}

function isTTSConfigured(tts) {
  if (tts?.default_provider === "auto" || tts?.provider === "auto") {
    return providerOrder(tts).some((provider) => isProviderConfigured(tts, provider) && withinLocalLimit(tts, provider, 0));
  }
  const provider = tts?.provider || tts?.default_provider || "custom";
  return isProviderConfigured(tts, provider) && withinLocalLimit(tts, provider, 0);
}

function isProviderConfigured(tts, provider) {
  if (provider === "google_chirp" || provider === "google_wavenet") {
    const google = tts?.google || {};
    return Boolean(google.client_email && google.private_key);
  }
  const custom = tts?.custom || {};
  return Boolean(custom.base_url && custom.api_key && custom.model && custom.voice);
}

function providerOrder(tts) {
  return uniqueProviders([...(tts?.auto_order || []), ...DEFAULT_CONFIG.tts.auto_order]);
}

function providerSummary(tts) {
  const provider = tts.provider || tts.default_provider || "auto";
  if (provider === "auto") {
    const first = providerOrder(tts).find((name) => isProviderConfigured(tts, name) && withinLocalLimit(tts, name, 0));
    return first ? `auto · ${providerLabels[first] || first}${usageSummary(tts, first)}` : "auto · 无可用 TTS";
  }
  if (provider === "custom") {
    return `${tts.custom.model} · ${tts.custom.voice}${usageSummary(tts, provider)}`;
  }
  return `${providerLabels[provider] || provider}${usageSummary(tts, provider)}`;
}

function uniqueProviders(values) {
  const seen = new Set();
  const out = [];
  for (const value of values || []) {
    const provider = String(value || "").trim();
    if (!provider || seen.has(provider)) continue;
    seen.add(provider);
    out.push(provider);
  }
  return out;
}

function buildLocalTTSRequest(tts, text) {
  const chars = Array.from(String(text || "")).length;
  const provider = tts.provider || tts.default_provider || "auto";
  if (provider !== "auto") {
    if (!isProviderConfigured(tts, provider) || !withinLocalLimit(tts, provider, chars)) {
      return null;
    }
    return { provider, tts };
  }
  const autoOrder = providerOrder(tts).filter((name) => isProviderConfigured(tts, name) && withinLocalLimit(tts, name, chars));
  if (!autoOrder.length) return null;
  return {
    provider: "auto",
    tts: {
      ...tts,
      auto_order: autoOrder,
    },
  };
}

function currentMonth() {
  return new Date().toISOString().slice(0, 7);
}

function providerUsed(provider) {
  return Number(state.usage?.[provider]?.[currentMonth()] || 0);
}

function providerLimit(tts, provider) {
  return Number(tts?.monthly_limits?.[provider] || 0);
}

function withinLocalLimit(tts, provider, chars) {
  const limit = providerLimit(tts, provider);
  return limit <= 0 || providerUsed(provider) + chars <= limit;
}

function addUsage(provider, chars) {
  const month = currentMonth();
  state.usage[provider] = state.usage[provider] || {};
  state.usage[provider][month] = providerUsed(provider) + chars;
  saveUsage();
}

function usageSummary(tts, provider) {
  const limit = providerLimit(tts, provider);
  if (limit <= 0) return "";
  return ` · 本月 ${formatNumber(providerUsed(provider))} / ${formatNumber(limit)}`;
}

async function analyze() {
  const query = $("query").value.trim();
  if (!query) {
    notice("先输入要分析的内容。");
    return;
  }
  if (!isLLMConfigured(state.config.llm)) {
    notice("先在配置里填写 LLM Base URL、Model 和 API Key。");
    return;
  }
  setBusy(true);
  notice("");
  try {
    const res = await fetch("/api/analyze", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query,
        accent: $("accent").value,
        translation_language: state.translationLanguage,
        llm: state.config.llm,
      }),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "分析失败");
    $("summary").textContent = data.summary || "已生成发音说明。";
    $("explanation").innerHTML = renderMarkdown(data.explanation_markdown || "");
    state.scriptLines = splitLines(data.tts_script || "");
    state.translationLines = splitLines(data.tts_script_translation || "");
    state.subtitleCueStops = buildSubtitleCueStops(state.scriptLines);
    setActiveSubtitle(-1);
    renderSubtitles();
  } catch (error) {
    notice(error.message);
  } finally {
    setBusy(false);
  }
}

async function playInput() {
  const text = $("query").value.trim();
  if (!text) {
    notice("先输入要发音的内容。");
    return;
  }
  await playTTS(text, "正在朗读输入内容...");
}

async function playScript() {
  const text = state.scriptLines.join("\n").trim();
  if (!text) {
    notice("还没有跟读稿，先分析一下。");
    return;
  }
  state.subtitleCueStops = buildSubtitleCueStops(state.scriptLines);
  await playTTS(text, "正在读稿...", true);
}

async function toggleScriptPlayback() {
  if (state.playbackSource === "script" && state.playbackState !== "idle") {
    stopAudio();
    return;
  }
  await playScript();
}

async function playTTS(text, loadingText, trackSubtitles = false) {
  if (!isTTSConfigured(state.config.tts)) {
    notice("先在配置里填写 TTS Base URL、API Key、Model 和 Voice。");
    return;
  }
  const ttsRequest = buildLocalTTSRequest(state.config.tts, text);
  if (!ttsRequest) {
    notice("本月 TTS 用量已达到本地限制，或没有可用的 TTS provider。");
    return;
  }
  stopAudio(false);
  if (trackSubtitles) setActiveSubtitle(0);
  const controller = new AbortController();
  state.audioAbortController = controller;
  state.playbackState = "loading";
  state.playbackSource = trackSubtitles ? "script" : "input";
  updateAudioControls();
  notice(loadingText);
  try {
    const res = await fetch("/api/tts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      signal: controller.signal,
      body: JSON.stringify({
        text,
        provider: ttsRequest.provider,
        language: "",
        speed: Number($("rate").value || 0.86),
        voice: state.config.tts.custom.voice,
        tts: ttsRequest.tts,
      }),
    });
    const contentType = res.headers.get("content-type") || "";
    if (!res.ok) {
      const data = contentType.includes("application/json") ? await res.json() : { error: await res.text() };
      throw new Error(data.error || "云端 TTS 不可用");
    }
    const blob = await res.blob();
    const audio = $("audio");
    if (state.audioUrl) URL.revokeObjectURL(state.audioUrl);
    state.audioUrl = URL.createObjectURL(blob);
    audio.src = state.audioUrl;
    audio.hidden = true;
    state.playbackState = "playing";
    updateAudioControls();
    await audio.play();
    const provider = res.headers.get("x-say-provider") || "cloud";
    const voice = res.headers.get("x-say-voice") || "";
    const chars = Number(res.headers.get("x-say-chars")) || Array.from(text).length;
    addUsage(provider, chars);
    notice(`正在朗读 ${providerLabels[provider] || provider}${voice ? ` · ${voice}` : ""}。`);
    renderStatus();
  } catch (error) {
    if (error.name === "AbortError") {
      return;
    }
    state.playbackState = "idle";
    state.playbackSource = "";
    updateAudioControls();
    notice(error.message);
    if (trackSubtitles) setActiveSubtitle(-1);
  } finally {
    if (state.audioAbortController === controller) {
      state.audioAbortController = null;
    }
    if (state.playbackState === "loading") {
      state.playbackState = "idle";
      state.playbackSource = "";
      updateAudioControls();
    }
  }
}

function stopAudio(resetNotice = true, resetSubtitles = state.playbackSource === "script") {
  if (state.audioAbortController) {
    state.audioAbortController.abort();
    state.audioAbortController = null;
  }
  const audio = $("audio");
  audio.pause();
  audio.currentTime = 0;
  state.playbackState = "idle";
  state.playbackSource = "";
  updateAudioControls();
  if (resetSubtitles) setActiveSubtitle(-1);
  if (resetNotice) notice("已停止。");
}

async function togglePause() {
  if (state.playbackSource !== "script" || !["playing", "paused"].includes(state.playbackState)) {
    return;
  }
  const audio = $("audio");
  if (state.playbackState === "playing") {
    audio.pause();
    state.playbackState = "paused";
    updateAudioControls();
    notice("已暂停。");
    return;
  }
  state.playbackState = "playing";
  updateAudioControls();
  await audio.play();
  notice("继续读稿。");
}

function updateAudioControls() {
  const playInputButton = $("play-input");
  const playScriptButton = $("play-script");
  const pauseScriptButton = $("pause-script");
  const inputActive = state.playbackSource === "input" && state.playbackState !== "idle";
  const scriptActive = state.playbackSource === "script" && state.playbackState !== "idle";
  const scriptPaused = state.playbackSource === "script" && state.playbackState === "paused";

  playInputButton.textContent = inputActive ? "停止" : "发音";
  playInputButton.classList.toggle("is-stop", inputActive);
  playInputButton.disabled = scriptActive;
  playInputButton.setAttribute("aria-pressed", inputActive ? "true" : "false");

  playScriptButton.textContent = scriptActive ? "停止" : "读稿";
  playScriptButton.classList.toggle("is-stop", scriptActive);
  playScriptButton.disabled = inputActive;
  playScriptButton.setAttribute("aria-pressed", scriptActive ? "true" : "false");

  pauseScriptButton.textContent = scriptPaused ? "恢复" : "暂停";
  pauseScriptButton.disabled = !(state.playbackSource === "script" && ["playing", "paused"].includes(state.playbackState));
  pauseScriptButton.setAttribute("aria-pressed", scriptPaused ? "true" : "false");
}

function syncSubtitle() {
  if (state.playbackSource !== "script") return;
  const audio = $("audio");
  if (!state.scriptLines.length || !Number.isFinite(audio.duration) || audio.duration <= 0) return;
  const ratio = Math.min(0.999, Math.max(0, audio.currentTime / audio.duration));
  const cueStops = state.subtitleCueStops.length === state.scriptLines.length
    ? state.subtitleCueStops
    : buildSubtitleCueStops(state.scriptLines);
  setActiveSubtitle(subtitleIndexForRatio(ratio, cueStops));
}

function handleAudioEnded() {
  const endedSource = state.playbackSource;
  state.playbackState = "idle";
  state.playbackSource = "";
  updateAudioControls();
  if (endedSource === "script") {
    setActiveSubtitle(-1);
    notice("读稿结束。");
    return;
  }
  notice("");
}

function buildSubtitleCueStops(lines) {
  const weights = lines.map(estimateSubtitleLineWeight);
  const total = weights.reduce((sum, weight) => sum + weight, 0) || lines.length || 1;
  let elapsed = 0;
  return weights.map((weight, index) => {
    elapsed += weight;
    return index === weights.length - 1 ? 1 : Math.min(0.999, elapsed / total);
  });
}

function subtitleIndexForRatio(ratio, cueStops) {
  if (!cueStops.length) return -1;
  const safeRatio = Math.min(0.999, Math.max(0, ratio));
  for (let index = 0; index < cueStops.length; index += 1) {
    if (safeRatio < cueStops[index]) return index;
  }
  return cueStops.length - 1;
}

function estimateSubtitleLineWeight(line) {
  const text = String(line || "").trim();
  if (!text) return 0.1;
  const latinWords = (text.match(/[A-Za-z]+(?:'[A-Za-z]+)?/g) || []).length;
  const cjkChars = (text.match(/[\u3400-\u9fff\uf900-\ufaff]/g) || []).length;
  const kanaChars = (text.match(/[\u3040-\u30ff\uff66-\uff9f]/g) || []).length;
  const hangulChars = (text.match(/[\uac00-\ud7af\u1100-\u11ff\u3130-\u318f]/g) || []).length;
  const digits = (text.match(/\d+/g) || []).length;
  const symbols = Math.max(0, text.replace(/[A-Za-z0-9\s\u3400-\u9fff\uf900-\ufaff\u3040-\u30ff\uff66-\uff9f\uac00-\ud7af\u1100-\u11ff\u3130-\u318f]/g, "").length);
  const pauses = (text.match(/[,.!?;:，。！？；：]/g) || []).length;
  return Math.max(
    0.8,
    latinWords + cjkChars * 0.56 + kanaChars * 0.58 + hangulChars * 0.58 + digits * 0.75 + symbols * 0.15 + pauses * 0.35 + 0.55,
  );
}

function setActiveSubtitle(index) {
  if (state.activeLine === index) return;
  state.activeLine = index;
  document.querySelectorAll(".subtitle-row").forEach((row) => {
    const active = Number(row.dataset.index) === index;
    row.classList.toggle("is-active", active);
    if (active) row.scrollIntoView({ block: "center", behavior: "smooth" });
  });
}

function renderSubtitles() {
  const board = $("subtitle-board");
  if (!state.scriptLines.length) {
    board.innerHTML = `<div class="subtitle-empty">分析后会生成双语对照跟读稿。</div>`;
    return;
  }
  board.innerHTML = state.scriptLines.map((line, index) => {
    const translation = state.translationLines[index] || "";
    return `
      <div class="subtitle-row" data-index="${index}">
        <div class="subtitle-en">${escapeHTML(line)}</div>
        <div class="subtitle-zh">${escapeHTML(translation)}</div>
      </div>`;
  }).join("");
}

function scriptClipboardText() {
  if (!state.scriptLines.length) return "";
  return state.scriptLines.map((line, index) => {
    const zh = state.translationLines[index] || "";
    return zh ? `${line}\n${zh}` : line;
  }).join("\n\n");
}

function splitLines(text) {
  return String(text || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function setBusy(isBusy) {
  $("loader").hidden = !isBusy;
  $("analyze-btn").disabled = isBusy;
}

function notice(text) {
  $("notice").textContent = text || "";
}

function renderMarkdown(markdown) {
  const lines = String(markdown || "").split(/\r?\n/);
  const html = [];
  let inList = false;
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      continue;
    }
    const heading = line.match(/^(#{2,3})\s+(.+)$/);
    if (heading) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      html.push(`<h3>${inlineMarkdown(heading[2])}</h3>`);
      continue;
    }
    const item = line.match(/^[-*]\s+(.+)$/);
    if (item) {
      if (!inList) {
        html.push("<ul>");
        inList = true;
      }
      html.push(`<li>${inlineMarkdown(item[1])}</li>`);
      continue;
    }
    if (inList) {
      html.push("</ul>");
      inList = false;
    }
    html.push(`<p>${inlineMarkdown(line)}</p>`);
  }
  if (inList) html.push("</ul>");
  return html.join("");
}

function inlineMarkdown(text) {
  return escapeHTML(text)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
}

function escapeHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function formatNumber(n) {
  return Number(n || 0).toLocaleString("zh-CN");
}

init();
