# Say Lab Demo

![Say Lab banner](docs/images/say-lab-banner.jpg)

A lightweight self-hosted pronunciation practice page demo with only static pages and the necessary request forwarding. Authorization and configuration stay in the local browser. After configuration, enter a word, phrase, sentence, or pronunciation question, let the LLM generate pronunciation notes and a bilingual read-along script, then play it with cloud TTS.

Public demo: [https://demo.saylab.ganfan.work/](https://demo.saylab.ganfan.work/)

中文文档: [README.md](README.md)

## What You Can Do

- Hear words, phrases, and sentences aloud
- Generate notes for IPA, stress, mouth position, linking, rhythm, and confusing sounds
- Generate a bilingual read-along script with playback highlighting
- Use OpenAI-compatible chat, Google TTS, or a custom speech provider

![Say Lab main interface screenshot](docs/images/say-lab-screenshot.jpg)

## Demo vs Full Backend Edition

| Capability | Demo Edition | Full Backend Edition |
| --- | --- | --- |
| API keys | Stored in the current browser | Managed by the server or operator environment |
| Configuration | Filled in separately per browser | Can be centrally configured and maintained |
| TTS usage tracking | Stored in the current browser's LocalStorage | Persisted by the backend, better for teams or long-term use |
| Google TTS | Users enter their own Service Account fields in the browser | Better managed on the backend so service account private keys are not exposed to the browser |
| Custom TTS | Users bring an OpenAI-compatible Speech API | Can be centrally connected and rate-limited by the backend |
| Deployment | Lightweight, useful for public demos and personal trials | Better for production services, shared team use, and stricter access control |

## Use The Demo

Open the site and click "配置" in the top-right corner. To use both analysis and playback, configure the LLM section and at least one speech provider: Google TTS or Custom TTS.

### LLM

| Setting | Required | Notes |
| --- | --- | --- |
| Base URL | Yes | OpenAI-compatible chat endpoint, such as `https://api.deepseek.com/v1` or `https://api.openai.com/v1` |
| Endpoint | No | Leave empty to use `{Base URL}/chat/completions` |
| Model | Yes | Chat model name, such as `deepseek-chat` |
| API Key | Yes | Key for the chat model |
| Timeout | No | Defaults to `90` seconds |

### Speech

For Custom TTS, fill in:

| Setting | Required | Notes |
| --- | --- | --- |
| Base URL | Yes | OpenAI-compatible API root, such as `https://api.openai.com/v1`; requests are sent to `{Base URL}/audio/speech` |
| API Key | Yes | Key for the speech provider |
| Model | Yes | Speech model, such as `gpt-4o-mini-tts` or a compatible model |
| Voice | Yes | Voice name, such as `alloy` or a provider-supported voice |
| Format / Speed / Timeout | No | Defaults to `mp3`, `1`, and `60` seconds |

For Google TTS, fill in:

| Setting | Required | Notes |
| --- | --- | --- |
| Client Email | Yes | The Service Account `client_email` |
| Private Key | Yes | The Service Account `private_key` |
| Project ID / Private Key ID | No | Useful for identifying the account; can be left empty |
| Token URL / TTS URL / Timeout | No | Defaults to Google's official endpoints and `60` seconds |

Google TTS also requires Cloud Text-to-Speech API to be enabled for the corresponding project.

`Default Provider` can stay on `auto`. `Auto Order` controls fallback order, and `Chirp / WaveNet / Custom Limit` controls the monthly character limit in the current browser.

## Run Locally

```bash
npm install
npm run dev
```

Then open:

```text
http://127.0.0.1:5567/
```

## Deploy To Cloudflare Workers

```bash
npm install
npm run deploy
```

Cloudflare hosts the page and provides same-origin API forwarding. You do not need to configure model keys in Cloudflare.

## Deploy Elsewhere

This demo needs only two capabilities:

- Static page hosting
- Same-origin `/api/analyze` and `/api/tts` forwarding

That means it can also run on Vercel, Netlify, Deno Deploy, a regular Node service, or a similar platform. Keep the same request format and the browser configuration experience will continue to work.

## Security Boundary

- API keys are stored in the user's own browser by default.
- The demo server does not store user configuration or usage records.
- During a request, keys are sent to the current API forwarding service, which forwards them to the selected model or speech provider.
- If you do not trust a public demo host, deploy your own copy before entering keys.
- Use the full backend edition when you need server-managed secrets, shared configuration, or reliable auditing.

## Development Checks

```bash
npm test
```
