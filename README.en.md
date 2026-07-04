# Say Lab

![Stars](https://img.shields.io/github/stars/Liu-Bot24/say-lab?style=flat&label=Stars&cache=20260704) ![Forks](https://img.shields.io/github/forks/Liu-Bot24/say-lab?style=flat&label=Forks&cache=20260704) ![Views 14d](https://github-stats.liu-qi.cn/api/badge/Liu-Bot24/say-lab/views14d.svg?v=4) ![Clones 14d](https://github-stats.liu-qi.cn/api/badge/Liu-Bot24/say-lab/clones14d.svg?v=4)

![Say Lab banner](docs/images/say-lab-banner.jpg)

This is a self-hosted pronunciation practice page. Enter a word, phrase, sentence, or pronunciation question, then let a chat model generate pronunciation notes and a bilingual read-along script with cloud TTS playback.

Demo: [https://demo.saylab.ganfan.work/](https://demo.saylab.ganfan.work/)

中文文档：[README.md](README.md)

## Features

- Read the input text aloud for quick pronunciation checks
- Generate pronunciation notes for confusing words or sounds
- Generate a bilingual read-along script with sentence highlighting
- Configure the chat model and TTS provider from the web UI

![Say Lab main interface screenshot](docs/images/say-lab-screenshot.jpg)

## Configuration

Say Lab reads `config.json` by default. You can also pass `-config /path/to/config.json` or set `SAY_CONFIG`. The web configuration panel reads and saves the same file; environment variables override matching config values.

Copy the example first:

```bash
cp config.example.json config.json
```

Example configuration. Do not include comments in production; keep it as standard JSON:

```jsonc
{
  // Service listen address. Keep it local when using a reverse proxy.
  "listen": "127.0.0.1:5567",

  // Monthly TTS usage counter.
  "data_file": "data/usage.json",

  // Chat model used for pronunciation notes and read-along scripts.
  "llm": {
    // OpenAI-compatible API base URL.
    "base_url": "https://api.deepseek.com/v1",

    // Optional. Empty means base_url + /chat/completions.
    "endpoint": "",

    "model": "deepseek-chat",
    "api_key": "",

    // Request timeout in seconds.
    "timeout": 90
  },

  // TTS settings for reading input text and generated scripts.
  "tts": {
    // auto selects a configured and available TTS provider.
    "default_provider": "auto",

    // Provider priority. If only custom TTS is configured, auto can stay unchanged.
    "auto_order": ["google_chirp", "google_wavenet"],

    // Monthly character limits. auto tries the next provider after a limit is reached.
    "monthly_limits": {
      "google_chirp": 800000,
      "google_wavenet": 4000000
    },

    // Google TTS. Fill the matching fields from a Google Cloud service account.
    "google": {
      "project_id": "",
      "client_email": "",
      "private_key": "",
      "private_key_id": "",
      "token_url": "https://oauth2.googleapis.com/token",
      "tts_url": "https://texttospeech.googleapis.com/v1/text:synthesize",
      "timeout": 60
    },

    // Google TTS relay, optional. It is not shown in the web configuration panel.
    "google_relay": {
      "endpoint": "",
      "relay_secret": "",
      "timeout": 60
    },

    // Custom TTS. To use only custom TTS, fill this section and keep default_provider as auto.
    "custom": {
      "base_url": "",
      "api_key": "",
      "model": "",
      "voice": "",
      "response_format": "mp3",
      "speed": 1,
      "timeout": 60
    }
  }
}
```

For English TTS, [Google Chirp 3 HD / WaveNet](https://console.cloud.google.com/apis/library/texttospeech.googleapis.com) usually provides 1M / 4M free characters per month, enough for most personal use.

If deployed on a server in mainland China, a relay service may be needed.

## Config Items

| Item | Purpose | Recommended Value |
| --- | --- | --- |
| `listen` | Service listen address | Keep `127.0.0.1:5567` behind a reverse proxy |
| `data_file` | Monthly TTS usage counter | Default: `data/usage.json` |
| `llm.*` | Chat model for pronunciation notes and scripts | Fill `base_url`, `model`, and `api_key` |
| `tts.default_provider` | Default speech provider | Usually keep `auto` |
| `tts.auto_order` | Provider order when `auto` is used | Default order: `google_chirp`, `google_wavenet`, `custom` |
| `tts.monthly_limits` | Monthly character limit by provider | When one reaches its limit, `auto` tries the next provider |
| `tts.google.*` | Google TTS | Fill `project_id`, `client_email`, and `private_key` |
| `tts.google_relay.*` | Google TTS relay | Fill it in config or environment variables only when needed |
| `tts.custom.*` | Custom TTS | OpenAI-compatible Speech API |

Common environment variables:

| Variable | Config Value |
| --- | --- |
| `SAY_CONFIG` | Config file path |
| `SAY_LLM_API_KEY` | `llm.api_key` |
| `SAY_LLM_BASE_URL` | `llm.base_url` |
| `SAY_LLM_ENDPOINT` | `llm.endpoint` |
| `SAY_LLM_MODEL` | `llm.model` |
| `SAY_LLM_TIMEOUT` | `llm.timeout` |
| `SAY_GOOGLE_PROJECT_ID` | `tts.google.project_id` |
| `SAY_GOOGLE_CLIENT_EMAIL` | `tts.google.client_email` |
| `SAY_GOOGLE_PRIVATE_KEY_ID` | `tts.google.private_key_id` |
| `SAY_GOOGLE_PRIVATE_KEY` | `tts.google.private_key` |
| `SAY_GOOGLE_TTS_URL` | `tts.google.tts_url` |
| `SAY_GOOGLE_RELAY_SECRET` | `tts.google_relay.relay_secret` |
| `SAY_GOOGLE_RELAY_ENDPOINT` | `tts.google_relay.endpoint` |
| `SAY_TTS_DEFAULT_PROVIDER` | `tts.default_provider` |
| `SAY_TTS_AUTO_ORDER` | `tts.auto_order` |
| `SAY_TTS_CUSTOM_API_KEY` | `tts.custom.api_key` |
| `SAY_TTS_CUSTOM_BASE_URL` | `tts.custom.base_url` |
| `SAY_TTS_CUSTOM_MODEL` | `tts.custom.model` |
| `SAY_TTS_CUSTOM_VOICE` | `tts.custom.voice` |

You can also configure the LLM, Google TTS, custom TTS, and TTS routing from the web UI.

![Say Lab configuration panel screenshot](docs/images/say-lab-config.jpg)

## Run

```bash
go run . -config config.json
```

Then open:

```text
http://127.0.0.1:5567/
```

For production, see `deploy/say-lab.service` and `deploy/nginx-say-lab.conf` for systemd and Nginx examples.
