# Say Lab Demo

![Say Lab 横幅](docs/images/say-lab-banner.jpg)

一个轻量自托管发音练习页面Demo，只带有静态页面和必要请求转发，授权和配置信息保存在本地浏览器。完成必要配置，输入单词、短语、句子或发音疑问，让大模型生成发音说明和双语跟读稿，再用云端 TTS 朗读。

公开Demo地址：[https://demo.saylab.ganfan.work/](https://demo.saylab.ganfan.work/)

English: [README.en.md](README.en.md)

## 你可以做什么

- 听单词、短语和句子的发音
- 生成音标、重音、口型、连读和易混音说明
- 生成双语跟读稿，并在播放时高亮当前句子
- 使用 OpenAI-compatible 聊天模型、Google TTS 或自定义语音服务

![Say Lab 主界面截图](docs/images/say-lab-screenshot.jpg)

## Demo 与完整后端版的区别

| 能力 | Demo 版本 | 完整后端版 |
| --- | --- | --- |
| API Key | 保存在当前浏览器 | 由服务端或运维环境管理 |
| 配置 | 每个浏览器各自填写 | 可集中配置并统一维护 |
| TTS 用量统计 | 保存在当前浏览器的 LocalStorage | 由后端持久化统计，更适合多人或长期使用 |
| Google TTS | 用户在浏览器填写自己的 Service Account 字段 | 适合放在后端管理，避免在浏览器暴露服务账号私钥 |
| 自定义 TTS | 用户自带 OpenAI-compatible Speech API | 可由后端统一接入和限额 |
| 部署复杂度 | 轻量，适合公开演示和个人试用 | 适合正式服务、团队使用和更严格的权限控制 |

## 使用方式

打开站点后，点右上角“配置”。要完整使用分析和朗读，至少需要填写大模型配置，并在 Google TTS 和自定义 TTS 中配置一种语音服务。

### 大模型配置

| 配置 | 是否必填 | 说明 |
| --- | --- | --- |
| Base URL | 必填 | OpenAI-compatible 聊天模型地址，例如 `https://api.deepseek.com/v1` 或 `https://api.openai.com/v1` |
| Endpoint | 可选 | 留空时自动使用 `{Base URL}/chat/completions` |
| Model | 必填 | 聊天模型名称，例如 `deepseek-chat` |
| API Key | 必填 | 调用聊天模型的 Key |
| Timeout | 可选 | 默认 `90` 秒 |

### 语音配置

使用自定义 TTS 时，填写：

| 配置 | 是否必填 | 说明 |
| --- | --- | --- |
| Base URL | 必填 | OpenAI-compatible API 根地址，例如 `https://api.openai.com/v1`，请求会自动发送到 `{Base URL}/audio/speech` |
| API Key | 必填 | 调用语音服务的 Key |
| Model | 必填 | 语音模型，例如 `gpt-4o-mini-tts` 或兼容模型 |
| Voice | 必填 | 语音名称，例如 `alloy` 或服务商支持的 voice |
| Format / Speed / Timeout | 可选 | 默认 `mp3`、`1`、`60` 秒 |

使用 Google TTS 时，填写：

| 配置 | 是否必填 | 说明 |
| --- | --- | --- |
| Client Email | 必填 | Google Service Account 的 `client_email` |
| Private Key | 必填 | Google Service Account 的 `private_key` |
| Project ID / Private Key ID | 可选 | 方便识别账号，可留空 |
| Token URL / TTS URL / Timeout | 可选 | 默认使用 Google 官方地址和 `60` 秒超时 |

Google TTS 还需要对应项目已启用 Cloud Text-to-Speech API。

`Default Provider` 可以保持 `auto`。`Auto Order` 控制自动尝试顺序，`Chirp / WaveNet / Custom Limit` 控制当前浏览器里的月度字符上限。

## 本地运行

```bash
npm install
npm run dev
```

然后打开：

```text
http://127.0.0.1:5567/
```

## 部署到 Cloudflare Workers

```bash
npm install
npm run deploy
```

部署后，Cloudflare 会托管页面并提供同源 API 转发。你不需要在 Cloudflare 里配置模型 Key。

## 部署到其他平台

这个 Demo 只需要两部分能力：

- 托管静态页面
- 提供同源 `/api/analyze` 和 `/api/tts` 转发

因此也可以部署到 Vercel、Netlify、Deno Deploy、普通 Node 服务或其他类似平台。保持同样的请求格式即可复用前端配置体验。

## 安全边界

- API Key 默认保存在用户自己的浏览器。
- Demo 服务器不保存用户配置或用量记录。
- 请求发生时，Key 会随请求发送给当前部署的 API 转发服务，再转发给模型或语音服务。
- 如果你不信任某个公开 Demo 站，请部署自己的副本后再填写 Key。
- 如果你需要服务端托管密钥、多人共享配置或可靠审计，请使用完整后端版。

## 开发检查

```bash
npm test
```
