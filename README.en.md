# Say Lab

![Say Lab banner](docs/images/say-lab-banner.png)

This is a self-hosted pronunciation practice page. Enter a word, phrase, sentence, or pronunciation question, then let a chat model generate pronunciation notes and a bilingual read-along script with cloud TTS playback.

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
    // auto selects an available provider from auto_order.
    "default_provider": "auto",

    // Provider order. For custom TTS only, use ["custom"].
    "auto_order": ["google_chirp", "google_wavenet"],

    // Monthly character limits. auto tries the next provider after a limit is reached.
    "monthly_limits": {
      "google_chirp": 800000,
      "google_wavenet": 4000000
    },

    // Google TTS relay, optional. Leave empty if you do not need a relay.
    "google_relay": {
      "endpoint": "",
      "relay_secret": "",
      "timeout": 60
    },

    // Custom TTS. Set default_provider to custom when using it.
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
| `tts.default_provider` | Default speech provider | Use `auto` for the Google example; use `custom` for custom TTS |
| `tts.auto_order` | Provider order when `auto` is used | Google example: `google_chirp`, `google_wavenet` |
| `tts.monthly_limits` | Monthly character limit by provider | When one reaches its limit, `auto` tries the next provider |
| `tts.google_relay.*` | Google TTS relay | Fill `endpoint` and `relay_secret` only when needed |
| `tts.custom.*` | Custom TTS | Fill it and set `tts.default_provider` to `custom` |

Common environment variables:

| Variable | Config Value |
| --- | --- |
| `SAY_CONFIG` | Config file path |
| `SAY_LLM_API_KEY` | `llm.api_key` |
| `SAY_GOOGLE_RELAY_SECRET` | `tts.google_relay.relay_secret` |
| `SAY_TTS_CUSTOM_API_KEY` | `tts.custom.api_key` |

You can also configure Say Lab from the web UI.

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
