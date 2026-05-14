const MAX_QUERY_CHARS = 4000;
const MAX_TTS_CHARS = 5000;
const MAX_ERROR_TEXT = 1200;

const DEFAULT_LLM = {
  base_url: "https://api.deepseek.com/v1",
  model: "deepseek-chat",
  timeout: 90,
};

const DEFAULT_TTS = {
  default_provider: "auto",
  provider: "auto",
  auto_order: ["google_chirp", "google_wavenet", "custom"],
  google: {
    token_url: "https://oauth2.googleapis.com/token",
    tts_url: "https://texttospeech.googleapis.com/v1/text:synthesize",
    timeout: 60,
  },
  custom: {
    response_format: "mp3",
    speed: 1,
    timeout: 60,
  },
};

export function createProxyHandler(options = {}) {
  const fetchImpl = options.fetch || globalThis.fetch?.bind(globalThis);
  if (!fetchImpl) {
    throw new Error("A fetch implementation is required");
  }

  return async function proxyHandler(request) {
    try {
      const url = new URL(request.url);
      if (request.method === "OPTIONS") {
        return new Response(null, { status: 204, headers: corsHeaders() });
      }
      if (url.pathname === "/api/status") {
        return handleStatus(request);
      }
      if (url.pathname === "/api/analyze") {
        return handleAnalyze(request, fetchImpl);
      }
      if (url.pathname === "/api/tts") {
        return handleTTS(request, fetchImpl);
      }
      return json({ error: "not found" }, 404);
    } catch (error) {
      return json({ error: error.message || "request failed" }, 500);
    }
  };
}

async function handleStatus(request) {
  if (request.method !== "GET") {
    return json({ error: "method not allowed" }, 405);
  }
  return json({
    mode: "stateless",
    storage: "browser",
    llm_configured: false,
    current_provider: null,
    providers: [
      {
        name: "custom",
        label: "OpenAI-compatible TTS",
        configured: false,
        used: 0,
        limit: 0,
      },
    ],
  });
}

async function handleAnalyze(request, fetchImpl) {
  if (request.method !== "POST") {
    return json({ error: "method not allowed" }, 405);
  }
  const body = await readJSON(request);
  const query = clean(body.query);
  if (!query) {
    return json({ error: "请输入要分析的单词、短语或问题" }, 400);
  }
  if (countChars(query) > MAX_QUERY_CHARS) {
    return json({ error: "输入太长，请控制在 4000 字以内" }, 400);
  }

  const llm = normalizeLLM(body.llm);
  const missing = missingLLMFields(llm);
  if (missing.length) {
    return json({ error: `请先配置 LLM ${missing.join(", ")}` }, 400);
  }

  const translationLanguage = clean(body.translation_language) || "Simplified Chinese";
  const payload = buildLLMPayload({
    query,
    accent: body.accent,
    notes: body.notes,
    translationLanguage,
    model: llm.model,
  });
  const endpoint = clean(llm.endpoint) || `${trimSlash(llm.base_url)}/chat/completions`;
  const response = await fetchImpl(endpoint, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${llm.api_key}`,
    },
    body: JSON.stringify(payload),
    signal: timeoutSignal(llm.timeout),
  });

  const responseBody = await response.text();
  if (!response.ok) {
    return json({
      error: `LLM 请求失败：${response.status} ${response.statusText} ${clip(responseBody)}`,
    }, 502);
  }

  let parsed;
  try {
    parsed = JSON.parse(responseBody);
  } catch {
    return json({ error: "LLM 返回了无效 JSON" }, 502);
  }
  const content = clean(parsed.choices?.[0]?.message?.content);
  if (!content) {
    return json({ error: "LLM 没有返回结果" }, 502);
  }

  let out;
  try {
    out = JSON.parse(extractJSONObject(content));
  } catch {
    out = {
      summary: "已生成发音说明。",
      explanation_markdown: content,
      tts_script: fallbackScript(query),
      tts_script_translation: fallbackScriptTranslation(query, translationLanguage),
      raw: content,
    };
  }
  if (!clean(out.tts_script)) {
    out.tts_script = fallbackScript(query);
  }
  if (!clean(out.tts_script_translation)) {
    out.tts_script_translation = fallbackScriptTranslation(out.tts_script, translationLanguage);
  }
  return json(out);
}

async function handleTTS(request, fetchImpl) {
  if (request.method !== "POST") {
    return json({ error: "method not allowed" }, 405);
  }
  const body = await readJSON(request);
  const text = clean(body.text);
  if (!text) {
    return json({ error: "没有可朗读的文本" }, 400);
  }
  if (countChars(text) > MAX_TTS_CHARS) {
    return json({ error: "朗读文本太长，请控制在 5000 字以内" }, 400);
  }

  const tts = normalizeTTS(body.tts);
  const requestedProvider = clean(body.provider) || tts.provider || tts.default_provider || "auto";
  const providers = providerOrder(requestedProvider, tts);
  const errors = [];
  let selectedProvider = "";
  let selectedVoice = "";
  let audioResponse = null;

  for (const provider of providers) {
    const missing = missingTTSFields(provider, tts);
    if (missing.length) {
      errors.push(`${provider}: 缺少 ${missing.join(", ")}`);
      continue;
    }
    try {
      const result = await callTTSProvider({
        fetchImpl,
        provider,
        text,
        language: body.language,
        speed: numberOr(body.speed, tts.custom.speed || 1),
        voice: body.voice,
        tts,
      });
      selectedProvider = provider;
      selectedVoice = result.voice || "";
      audioResponse = result.response;
      break;
    } catch (error) {
      errors.push(`${provider}: ${error.message || "请求失败"}`);
      if (requestedProvider !== "auto") break;
    }
  }

  if (!audioResponse) {
    const prefix = requestedProvider === "auto" ? "没有可用的云端 TTS" : `${requestedProvider} 不可用`;
    return json({ error: `${prefix}${errors.length ? `：${errors.join("；")}` : ""}` }, 400);
  }

  const headers = new Headers(audioResponse.headers);
  headers.set("cache-control", "no-store");
  headers.set("x-say-provider", selectedProvider);
  if (selectedVoice) {
    headers.set("x-say-voice", selectedVoice);
  }
  headers.set("x-say-chars", String(countChars(text)));
  return new Response(audioResponse.body, {
    status: audioResponse.status,
    statusText: audioResponse.statusText,
    headers,
  });
}

function buildLLMPayload({ query, accent, notes, translationLanguage, model }) {
  const selectedAccent = clean(accent) || "Auto-detect from the user's text; use a standard pronunciation for the detected language unless the user asks for a specific accent";
  const system = `You are Say Lab, a precise and practical pronunciation coach for language learners.
Your job is to analyze the user's target text, explain the pronunciation clearly, and produce a short TTS-friendly practice script with a side-by-side reference.

Core rules:
- Prioritize phonetic accuracy over sounding confident. If a pronunciation is uncertain or has regional variation, say so briefly.
- Infer the target pronunciation language and accent from the user's text and the stated preference. Do not assume the target is English when the input is clearly another language.
- Explain in Simplified Chinese by default, while keeping IPA, example words, and language-specific terms when useful.
- Focus only on pronunciation details that matter for the user's input: sounds, stress, rhythm, linking, intonation, mouth shape, tongue/lip position, and common listening or ASR confusions.
- Do not add example-specific rules unless the user's input contains those examples.
- Keep the practice script natural for TTS: short lines, minimal symbols, no markdown bullets, and no long meta-explanations.

Output responsibilities:
- tts_script is the text that will be spoken by TTS. It must use the target pronunciation language, not the selected reference language.
- tts_script_translation is the side-by-side reference. It must use the selected reference language: ${translationLanguage}. It must match tts_script line count and line order, and must not change the language of tts_script.

Return STRICT JSON only, without markdown fences. The JSON schema is:
{
  "summary": "one short Chinese sentence",
  "explanation_markdown": "Markdown explanation in Chinese with IPA, key mouth/tongue/vowel/consonant differences, common ASR confusion reasons, and 3 short practice sentences",
  "tts_script": "A clean script intended to be read aloud by TTS. Use the target pronunciation language from the user's input or stated preference. Include listen-and-repeat pacing, minimal symbols, and short lines.",
  "tts_script_translation": "Line-by-line ${translationLanguage} meaning notes or translation for tts_script. Keep the same line count and line order."
}
The tts_script_translation is not read aloud; it is only displayed as a side-by-side reference.`;
  const user = `我要学习下面内容的发音：
${query}

目标发音语言或口音偏好：
${selectedAccent}

右侧对照语言：${translationLanguage}

补充说明：
${clean(notes)}`;
  return {
    model,
    messages: [
      { role: "system", content: system },
      { role: "user", content: user },
    ],
    temperature: 0.2,
    max_tokens: 1800,
  };
}

async function callTTSProvider({ fetchImpl, provider, text, language, speed, voice, tts }) {
  if (provider === "custom") {
    return {
      response: await callCustomTTS({
        fetchImpl,
        text,
        speed,
        voice,
        custom: tts.custom,
      }),
      voice: clean(voice) || tts.custom.voice,
    };
  }
  if (provider === "google_chirp" || provider === "google_wavenet") {
    return callGoogleTTS({
      fetchImpl,
      provider,
      text,
      language,
        speed,
        voice,
        google: tts.google,
      });
  }
  throw new Error(`未知 TTS provider: ${provider}`);
}

async function callCustomTTS({ fetchImpl, text, speed, voice, custom }) {
  const selectedVoice = clean(voice) || custom.voice;
  const payload = {
    model: custom.model,
    input: text,
    voice: selectedVoice,
    response_format: custom.response_format || "mp3",
    speed,
  };
  const response = await fetchImpl(`${trimSlash(custom.base_url)}/audio/speech`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${custom.api_key}`,
    },
    body: JSON.stringify(payload),
    signal: timeoutSignal(custom.timeout),
  });
  if (!response.ok) {
    const textBody = await response.text();
    throw new Error(`自定义 TTS 请求失败：${response.status} ${response.statusText} ${clip(textBody)}`);
  }
  return response;
}

async function callGoogleTTS({ fetchImpl, provider, text, language, speed, voice, google }) {
  return callGoogleDirectTTS({ fetchImpl, provider, text, language, speed, voice, google });
}

async function callGoogleDirectTTS({ fetchImpl, provider, text, language, speed, voice, google }) {
  const selectedLanguage = clean(language) || guessTTSLanguage(text);
  const tier = provider === "google_chirp" ? "chirp3-hd" : "wavenet";
  const voiceName = googleVoiceName(clean(voice), selectedLanguage, tier);
  const audioConfig = { audioEncoding: "MP3" };
  if (tier !== "chirp3-hd" && speed > 0) {
    audioConfig.speakingRate = speed;
  }
  const payload = {
    input: { text },
    voice: {
      languageCode: selectedLanguage,
      name: voiceName,
    },
    audioConfig,
  };
  const token = await googleAccessToken(fetchImpl, google);
  const response = await fetchImpl(google.tts_url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(payload),
    signal: timeoutSignal(google.timeout),
  });
  const responseBody = await response.text();
  if (!response.ok) {
    throw new Error(`Google TTS 请求失败：${response.status} ${response.statusText} ${clip(responseBody)}`);
  }
  let parsed;
  try {
    parsed = JSON.parse(responseBody);
  } catch {
    throw new Error("Google TTS 返回了无效 JSON");
  }
  if (!parsed.audioContent) {
    throw new Error("Google TTS 响应缺少 audioContent");
  }
  return {
    response: new Response(base64ToBytes(parsed.audioContent), {
      status: 200,
      headers: { "content-type": "audio/mpeg" },
    }),
    voice: voiceName,
  };
}

function normalizeLLM(input = {}) {
  return {
    ...DEFAULT_LLM,
    ...input,
    base_url: clean(input.base_url || input.baseUrl || DEFAULT_LLM.base_url),
    endpoint: clean(input.endpoint),
    model: clean(input.model || DEFAULT_LLM.model),
    api_key: clean(input.api_key || input.apiKey),
    timeout: numberOr(input.timeout, DEFAULT_LLM.timeout),
  };
}

function normalizeTTS(input = {}) {
  const custom = {
    ...DEFAULT_TTS.custom,
    ...(input.custom || {}),
  };
  const google = {
    ...DEFAULT_TTS.google,
    ...(input.google || {}),
  };
  return {
    ...DEFAULT_TTS,
    ...input,
    default_provider: clean(input.default_provider || input.provider || DEFAULT_TTS.default_provider),
    provider: clean(input.provider || input.default_provider || DEFAULT_TTS.provider),
    auto_order: normalizeAutoOrder(input.auto_order || input.autoOrder || DEFAULT_TTS.auto_order),
    google: {
      ...google,
      project_id: clean(google.project_id || google.projectId),
      client_email: clean(google.client_email || google.clientEmail),
      private_key: clean(google.private_key || google.privateKey),
      private_key_id: clean(google.private_key_id || google.privateKeyId),
      token_url: clean(google.token_url || google.tokenUrl || DEFAULT_TTS.google.token_url),
      tts_url: clean(google.tts_url || google.ttsUrl || DEFAULT_TTS.google.tts_url),
      timeout: numberOr(google.timeout, DEFAULT_TTS.google.timeout),
    },
    custom: {
      ...custom,
      base_url: clean(custom.base_url || custom.baseUrl),
      api_key: clean(custom.api_key || custom.apiKey),
      model: clean(custom.model),
      voice: clean(custom.voice),
      response_format: clean(custom.response_format) || DEFAULT_TTS.custom.response_format,
      speed: numberOr(custom.speed, DEFAULT_TTS.custom.speed),
      timeout: numberOr(custom.timeout, DEFAULT_TTS.custom.timeout),
    },
  };
}

function normalizeAutoOrder(order) {
  const input = Array.isArray(order) ? order : splitCSV(order);
  return uniqueProviders(input.length ? input : DEFAULT_TTS.auto_order);
}

function providerOrder(requestedProvider, tts) {
  if (requestedProvider !== "auto") {
    return uniqueProviders([requestedProvider]);
  }
  return uniqueProviders([...(tts.auto_order || []), ...DEFAULT_TTS.auto_order]);
}

function missingLLMFields(llm) {
  return [
    [llm.base_url, "Base URL"],
    [llm.model, "Model"],
    [llm.api_key, "API Key"],
  ].filter(([value]) => !value).map(([, label]) => label);
}

function missingCustomTTSFields(custom) {
  return [
    [custom.base_url, "Base URL"],
    [custom.api_key, "API Key"],
    [custom.model, "Model"],
    [custom.voice, "Voice"],
  ].filter(([value]) => !value).map(([, label]) => label);
}

function missingTTSFields(provider, tts) {
  if (provider === "custom") {
    return missingCustomTTSFields(tts.custom);
  }
  if (provider === "google_chirp" || provider === "google_wavenet") {
    if (googleDirectConfigured(tts.google)) {
      return [];
    }
    return ["Google service account"];
  }
  return ["provider"];
}

function googleDirectConfigured(google) {
  return Boolean(google?.client_email && google?.private_key);
}

async function readJSON(request) {
  try {
    return await request.json();
  } catch {
    throw new Error("invalid json body");
  }
}

function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      ...corsHeaders(),
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}

function corsHeaders() {
  return {
    "access-control-allow-origin": "*",
    "access-control-allow-methods": "GET, POST, OPTIONS",
    "access-control-allow-headers": "content-type",
  };
}

function timeoutSignal(seconds) {
  const ms = Math.max(1, numberOr(seconds, 60)) * 1000;
  if (globalThis.AbortSignal?.timeout) {
    return AbortSignal.timeout(ms);
  }
  const controller = new AbortController();
  setTimeout(() => controller.abort(), ms);
  return controller.signal;
}

function extractJSONObject(text) {
  let value = clean(text);
  if (value.startsWith("```json")) value = value.slice(7);
  if (value.startsWith("```")) value = value.slice(3);
  if (value.endsWith("```")) value = value.slice(0, -3);
  value = clean(value);
  const start = value.indexOf("{");
  const end = value.lastIndexOf("}");
  if (start >= 0 && end > start) {
    return value.slice(start, end + 1);
  }
  return value;
}

function fallbackScript(query) {
  const text = clean(query);
  return `Listen and repeat.\n${text}\nAgain.\n${text}`;
}

function fallbackScriptTranslation(query, translationLanguage) {
  const text = clean(query);
  if (clean(translationLanguage).toLowerCase().includes("english")) {
    return `Listen and repeat.\n${text}\nAgain.\n${text}`;
  }
  return `听并跟读。\n${text}\n再来一次。\n${text}`;
}

async function googleAccessToken(fetchImpl, google) {
  if (!google.client_email || !google.private_key) {
    throw new Error("Google TTS 缺少 client_email 或 private_key");
  }
  const now = Math.floor(Date.now() / 1000);
  const assertion = await signGoogleJWT(google, now);
  const form = new URLSearchParams({
    grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer",
    assertion,
  });
  const response = await fetchImpl(google.token_url, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: form.toString(),
    signal: timeoutSignal(google.timeout),
  });
  const responseBody = await response.text();
  if (!response.ok) {
    throw new Error(`Google token 请求失败：${response.status} ${response.statusText} ${clip(responseBody)}`);
  }
  let parsed;
  try {
    parsed = JSON.parse(responseBody);
  } catch {
    throw new Error("Google token 返回了无效 JSON");
  }
  if (!parsed.access_token) {
    throw new Error("Google token 响应缺少 access_token");
  }
  return parsed.access_token;
}

async function signGoogleJWT(google, now) {
  const header = { alg: "RS256", typ: "JWT" };
  const claim = {
    iss: google.client_email,
    scope: "https://www.googleapis.com/auth/cloud-platform",
    aud: google.token_url,
    exp: now + 3600,
    iat: now,
  };
  const signingInput = `${base64URLJSON(header)}.${base64URLJSON(claim)}`;
  const keyData = pemToArrayBuffer(google.private_key);
  const privateKey = await crypto.subtle.importKey(
    "pkcs8",
    keyData,
    { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const signature = await crypto.subtle.sign(
    "RSASSA-PKCS1-v1_5",
    privateKey,
    new TextEncoder().encode(signingInput),
  );
  return `${signingInput}.${base64URLBytes(new Uint8Array(signature))}`;
}

function googleVoiceName(requestedVoice, languageCode, tier) {
  if (requestedVoice && requestedVoice.includes("Google")) {
    return requestedVoice;
  }
  return defaultGoogleVoiceName(languageCode, tier);
}

function defaultGoogleVoiceName(languageCode, tier) {
  if (tier.includes("chirp")) {
    const voices = {
      "ar-XA": "ar-XA-Chirp3-HD-Charon",
      "bn-IN": "bn-IN-Chirp3-HD-Charon",
      "cmn-CN": "cmn-CN-Chirp3-HD-Charon",
      "de-DE": "de-DE-Chirp3-HD-Charon",
      "en-GB": "en-GB-Chirp3-HD-Charon",
      "en-US": "en-US-Chirp3-HD-Charon",
      "es-ES": "es-ES-Chirp3-HD-Charon",
      "fr-FR": "fr-FR-Chirp3-HD-Charon",
      "hi-IN": "hi-IN-Chirp3-HD-Charon",
      "ja-JP": "ja-JP-Chirp3-HD-Charon",
      "ko-KR": "ko-KR-Chirp3-HD-Charon",
      "pt-BR": "pt-BR-Chirp3-HD-Charon",
      "ru-RU": "ru-RU-Chirp3-HD-Charon",
    };
    return voices[languageCode] || "en-US-Chirp3-HD-Charon";
  }
  const voices = {
    "ar-XA": "ar-XA-Wavenet-B",
    "bn-IN": "bn-IN-Wavenet-B",
    "cmn-CN": "cmn-CN-Wavenet-A",
    "de-DE": "de-DE-Wavenet-B",
    "en-GB": "en-GB-Wavenet-B",
    "en-US": "en-US-Wavenet-D",
    "es-ES": "es-ES-Wavenet-B",
    "fr-FR": "fr-FR-Wavenet-B",
    "hi-IN": "hi-IN-Wavenet-D",
    "ja-JP": "ja-JP-Wavenet-B",
    "ko-KR": "ko-KR-Wavenet-A",
    "pt-BR": "pt-BR-Wavenet-A",
    "ru-RU": "ru-RU-Wavenet-A",
  };
  return voices[languageCode] || "en-US-Wavenet-D";
}

function guessTTSLanguage(text) {
  if (/[\u3040-\u30ff\uff66-\uff9f]/u.test(text)) return "ja-JP";
  if (/[\uac00-\ud7af\u1100-\u11ff\u3130-\u318f]/u.test(text)) return "ko-KR";
  if (/[\u0400-\u04ff]/u.test(text)) return "ru-RU";
  if (/[\u0600-\u06ff\u0750-\u077f\u08a0-\u08ff]/u.test(text)) return "ar-XA";
  if (/[\u0900-\u097f]/u.test(text)) return "hi-IN";
  if (/[\u0980-\u09ff]/u.test(text)) return "bn-IN";
  if (/[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/u.test(text)) return "cmn-CN";
  const lower = text.toLowerCase();
  if (/\b(hola|gracias|porque|para|estoy|senor|señor|manana|mañana)\b/u.test(lower) || /[¿¡ñ]/u.test(text)) return "es-ES";
  if (/\b(bonjour|merci|pourquoi|avec|francais|français|tres|très)\b/u.test(lower) || /[àâæçèêëîïôœùûÿ]/u.test(text)) return "fr-FR";
  if (/\b(guten|danke|nicht|bitte|deutsch|sprechen)\b/u.test(lower) || /[äöüß]/u.test(text)) return "de-DE";
  if (/\b(ola|olá|obrigado|obrigada|voce|você|estou|portugues|português)\b/u.test(lower) || /[ãõ]/u.test(text)) return "pt-BR";
  return "en-US";
}

function base64URLJSON(value) {
  return base64URLBytes(new TextEncoder().encode(JSON.stringify(value)));
}

function base64URLBytes(bytes) {
  return bytesToBase64(bytes).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

function base64ToBytes(value) {
  const binary = atob(clean(value));
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

function bytesToBase64(bytes) {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function pemToArrayBuffer(pem) {
  const base64 = clean(pem)
    .replaceAll("\\n", "\n")
    .replace(/-----BEGIN [^-]+-----/g, "")
    .replace(/-----END [^-]+-----/g, "")
    .replace(/\s+/g, "");
  return base64ToBytes(base64);
}

function clean(value) {
  return String(value ?? "").trim();
}

function trimSlash(value) {
  return clean(value).replace(/\/+$/, "");
}

function numberOr(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function splitCSV(value) {
  return String(value || "")
    .split(",")
    .map((item) => clean(item))
    .filter(Boolean);
}

function uniqueProviders(values) {
  const seen = new Set();
  const out = [];
  for (const value of values || []) {
    const provider = clean(value);
    if (!provider || seen.has(provider)) continue;
    seen.add(provider);
    out.push(provider);
  }
  return out;
}

function countChars(value) {
  return Array.from(String(value || "")).length;
}

function clip(value) {
  const text = clean(value);
  return text.length > MAX_ERROR_TEXT ? `${text.slice(0, MAX_ERROR_TEXT)}...` : text;
}
