import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { describe, test } from "node:test";

import { createProxyHandler } from "./proxy-core.js";

const TEST_PRIVATE_KEY = `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7QjRZmNJg/iAc
lS44DvazwFX/OekfqZRZaNLbwZuzDo2z8A7wcQH8zKdrwqbNVuFM7RNTXyJMbYwM
dLoSOsDhcRx87XWzna91HNzq58Q87yBjhbeATLngp4bG3NcNM9uHDgfhADVY4ZmT
xdVxVBPGdvd9ebpbMczcEMEm/tXBjk4MPc5x3NtuxsxwomyJN0rKrcVQ/PqiQuNv
AHNfp8BehuvsrRoFrqZtomgFP9tbP7heGpicchic3vG3TTxQWDke/223jhMbuGre
0S0CEVYp+0WuAuX6sfYmJ352W/6TWjsjw3p19cv4Wo6tXO2wokOct8pHiQPxkDoF
dP5fe8tbAgMBAAECggEAATug25wpenEB5h5HVSAoGKviBRGtCsPTkbkidkbyjv3A
ChjBCG0+DZuNWKTJ810ok5iWAX0FXUp1Wxmjpf4UCgf0H5Byr3GQ160OeU94V8xd
Qrp9KE+ZA7UmqoWGtvQJ9HwvqhgVwdWObM4nJx1eGfbrjzxvcS1FQ4eqteaEoscZ
rAfJRgvrX+bKSqxz+xN4Yr4LSPs0ST4kTxZHvkPAnGWEywLXy+DW1qj5wUU+pWaj
iqy2OYO0Rnl4dNPDAK9Yz7kmlvwl32EBlFKEbUX8t8nHKFc3wIsmheeAnKKg7Z7m
NDwqxiLnOed4PyV22Nf5HG7MjPCsyqszHhx4RtxPGQKBgQD8DVT6152Q3PTZYAIU
sSrB1RA5enCgvGunsKpWg+FfTkf9NdL0mbpwrEUFTAPALZHffWPMevlUuC+MCr6l
vlc4dI8xVg0x80a1iOoUp5hzeTHuqJ9+OivN7odaCgrSzt1rQ2GLaOHdV2tOCiq+
hYlWK0pyT2ddjbnpvQlU1vRBaQKBgQC+MRD9OzVAlw/VIDP6YpvVqbeiXh5vlX6B
6UvUv5jFPUIVQn0dk+t6k3700icYIClIhmKciL6Zqhnui4AnXLKxH+4fOsybboXn
kcQ9jrtWg2cvM3D+4hokCCXqyxlddH9KAyp088IkI1b8o14XQSfJTxH9Dj7hN6Sa
2gTLc2rKIwKBgGks2wANBufS+6zVPikKQLA/SxTg/axk03tW4c1WHK4bSTjkw9Fj
cIPhrq9mJ86CdXNPrFxAGFh8vwJt3V3mDhk1sibBJKJqRGAt8JeL07ajpO1UmbEf
bvXwibRNqnSK86P1qvd0XYNyHgmjfnCf4k39pIQMlDJamHBrfEBVrfi5AoGATcrX
/hwXngGr0irH1+Q5hV1T5S/GFzH4er6n668ql5KE0xlM/6ofKRXnWdATeYS+HhIJ
h6lO/J9c1/trQa5i0JwU0+VDB1hfoOY4dE1LRwmcTOwnjbmBo7dJew3zpAwgfG9r
EEcb45go3lEcDTrzQR38Z5tKEOinhUcjquvchC0CgYEAqVghccPq1p70m3fUjsU2
LiPqJWmVmWjBPJugPARrMn87/9UKtrcuEBumjBSiPHUiq4LeeJUdSyr87rE5m3z6
RYOEXGX7Kk6qBuAXcCLN8A+4EWdu2H06X35AjpX/QRxXB/54gc03BwxR/vyLoLai
nb9hAPcD82MpOR6l6WcEhXo=
-----END PRIVATE KEY-----`;

function jsonRequest(path, body) {
  return new Request(`https://say-lab.test${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
}

describe("proxy core", () => {
  function googlePrivateKeyPEM() {
    const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
    return privateKey.export({ type: "pkcs8", format: "pem" });
  }

  test("forwards pronunciation analysis with client-supplied LLM credentials", async () => {
    let captured;
    const handler = createProxyHandler({
      fetch: async (url, init) => {
        captured = { url, init, body: JSON.parse(init.body) };
        return Response.json({
          choices: [
            {
              message: {
                content: JSON.stringify({
                  summary: "已生成说明。",
                  explanation_markdown: "## IPA\n/skill/",
                  tts_script: "Listen.\nskill.",
                  tts_script_translation: "听。\nskill。",
                }),
              },
            },
          ],
        });
      },
    });

    const response = await handler(jsonRequest("/api/analyze", {
      query: "skill vs scale",
      accent: "American English",
      translation_language: "English",
      llm: {
        base_url: "https://llm.example/v1",
        model: "demo-chat",
        api_key: "llm-key",
        timeout: 30,
      },
    }));

    assert.equal(response.status, 200);
    assert.equal(captured.url, "https://llm.example/v1/chat/completions");
    assert.equal(captured.init.method, "POST");
    assert.equal(captured.init.headers.authorization, "Bearer llm-key");
    assert.equal(captured.body.model, "demo-chat");
    assert.match(captured.body.messages[0].content, /selected reference language: English/);
    assert.match(captured.body.messages[1].content, /skill vs scale/);

    const payload = await response.json();
    assert.equal(payload.summary, "已生成说明。");
    assert.equal(payload.tts_script, "Listen.\nskill.");
  });

  test("falls back when the chat model returns non-json content", async () => {
    const handler = createProxyHandler({
      fetch: async () => Response.json({
        choices: [
          {
            message: {
              content: "## Pronunciation\nKeep the vowel short.",
            },
          },
        ],
      }),
    });

    const response = await handler(jsonRequest("/api/analyze", {
      query: "ship",
      llm: {
        base_url: "https://llm.example/v1",
        model: "demo-chat",
        api_key: "llm-key",
      },
    }));

    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.equal(payload.summary, "已生成发音说明。");
    assert.equal(payload.explanation_markdown, "## Pronunciation\nKeep the vowel short.");
    assert.match(payload.tts_script, /ship/);
    assert.equal(payload.raw, "## Pronunciation\nKeep the vowel short.");
  });

  test("forwards custom speech requests with client-supplied TTS credentials", async () => {
    let captured;
    const audio = new Uint8Array([1, 2, 3, 4]);
    const handler = createProxyHandler({
      fetch: async (url, init) => {
        captured = { url, init, body: JSON.parse(init.body) };
        return new Response(audio, {
          status: 200,
          headers: { "content-type": "audio/mpeg" },
        });
      },
    });

    const response = await handler(jsonRequest("/api/tts", {
      text: "hello",
      speed: 0.9,
      tts: {
        provider: "custom",
        custom: {
          base_url: "https://tts.example/v1",
          api_key: "tts-key",
          model: "voice-model",
          voice: "alloy",
          response_format: "mp3",
          timeout: 60,
        },
      },
    }));

    assert.equal(response.status, 200);
    assert.equal(captured.url, "https://tts.example/v1/audio/speech");
    assert.equal(captured.init.headers.authorization, "Bearer tts-key");
    assert.deepEqual(captured.body, {
      model: "voice-model",
      input: "hello",
      voice: "alloy",
      response_format: "mp3",
      speed: 0.9,
    });
    assert.equal(response.headers.get("content-type"), "audio/mpeg");
    assert.equal(response.headers.get("x-say-provider"), "custom");
    assert.equal(response.headers.get("x-say-chars"), "5");
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), audio);
  });

  test("auto speech falls back from failed Google TTS to custom TTS", async () => {
    const calls = [];
    const audio = new Uint8Array([9, 8, 7]);
    const handler = createProxyHandler({
      fetch: async (url, init) => {
        calls.push({ url, init, body: String(init.body).startsWith("{") ? JSON.parse(init.body) : init.body });
        if (url === "https://google.example/token") {
          return Response.json({ access_token: "google-token", expires_in: 3600 });
        }
        if (url === "https://google.example/v1/text:synthesize") {
          return Response.json({ error: { message: "quota exceeded" } }, { status: 429 });
        }
        return new Response(audio, {
          status: 200,
          headers: { "content-type": "audio/mpeg" },
        });
      },
    });

    const response = await handler(jsonRequest("/api/tts", {
      text: "hello",
      speed: 0.9,
      tts: {
        default_provider: "auto",
        auto_order: ["google_chirp", "custom"],
        google: {
          client_email: "tts@example.iam.gserviceaccount.com",
          private_key: TEST_PRIVATE_KEY,
          token_url: "https://google.example/token",
          tts_url: "https://google.example/v1/text:synthesize",
          timeout: 60,
        },
        custom: {
          base_url: "https://tts.example/v1",
          api_key: "tts-key",
          model: "voice-model",
          voice: "alloy",
          response_format: "mp3",
          timeout: 60,
        },
      },
    }));

    assert.equal(response.status, 200);
    assert.equal(response.headers.get("x-say-provider"), "custom");
    assert.equal(calls.length, 3);
    assert.equal(calls[0].url, "https://google.example/token");
    assert.equal(calls[1].url, "https://google.example/v1/text:synthesize");
    assert.equal(calls[1].init.headers.authorization, "Bearer google-token");
    assert.equal(calls[2].url, "https://tts.example/v1/audio/speech");
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), audio);
  });

  test("ignores backend-only Google relay config in the browser demo", async () => {
    const calls = [];
    const audio = new Uint8Array([5, 4, 3]);
    const handler = createProxyHandler({
      fetch: async (url, init) => {
        calls.push({ url, init, body: JSON.parse(init.body) });
        return new Response(audio, {
          status: 200,
          headers: { "content-type": "audio/mpeg" },
        });
      },
    });

    const response = await handler(jsonRequest("/api/tts", {
      text: "hello",
      tts: {
        default_provider: "auto",
        auto_order: ["google_chirp", "custom"],
        google_relay: {
          endpoint: "https://relay.example/v1/tts",
          relay_secret: "relay-secret",
        },
        custom: {
          base_url: "https://tts.example/v1",
          api_key: "tts-key",
          model: "voice-model",
          voice: "alloy",
        },
      },
    }));

    assert.equal(response.status, 200);
    assert.equal(response.headers.get("x-say-provider"), "custom");
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "https://tts.example/v1/audio/speech");
  });

  test("forwards Google speech requests with browser-supplied Google TTS config", async () => {
    let captured;
    let sawTokenRequest = false;
    const handler = createProxyHandler({
      fetch: async (url, init) => {
        if (url === "https://google.example/token") {
          sawTokenRequest = true;
          assert.match(String(init.body), /grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Ajwt-bearer/);
          assert.match(String(init.body), /assertion=/);
          return Response.json({ access_token: "google-token", expires_in: 3600 });
        }
        captured = { url, init, body: JSON.parse(init.body) };
        return Response.json({
          audioContent: btoa("google-audio"),
        });
      },
    });

    const response = await handler(jsonRequest("/api/tts", {
      text: "hello",
      language: "en-US",
      speed: 0.9,
      tts: {
        default_provider: "google_chirp",
        google: {
          client_email: "tts@example.iam.gserviceaccount.com",
          private_key: TEST_PRIVATE_KEY,
          token_url: "https://google.example/token",
          tts_url: "https://google.example/v1/text:synthesize",
          timeout: 60,
        },
      },
    }));

    assert.equal(response.status, 200);
    assert.equal(sawTokenRequest, true);
    assert.equal(captured.url, "https://google.example/v1/text:synthesize");
    assert.equal(captured.init.headers.authorization, "Bearer google-token");
    assert.equal(captured.body.voice.name, "en-US-Chirp3-HD-Charon");
    assert.equal(captured.body.audioConfig.audioEncoding, "MP3");
    assert.equal(response.headers.get("x-say-provider"), "google_chirp");
    assert.equal(response.headers.get("x-say-voice"), "en-US-Chirp3-HD-Charon");
    assert.equal(await response.text(), "google-audio");
  });

  test("rejects missing credentials before calling providers", async () => {
    let calls = 0;
    const handler = createProxyHandler({
      fetch: async () => {
        calls += 1;
        return Response.json({});
      },
    });

    const response = await handler(jsonRequest("/api/analyze", {
      query: "hello",
      llm: {
        base_url: "https://llm.example/v1",
        model: "demo-chat",
      },
    }));

    assert.equal(response.status, 400);
    assert.equal(calls, 0);
    const payload = await response.json();
    assert.match(payload.error, /API Key/);
  });
});
