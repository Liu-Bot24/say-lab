package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestChooseProviderPrefersGoogleChirpThenFallsBackToWaveNet(t *testing.T) {
	usage := &UsageStore{Data: map[string]map[string]int{}}
	cfg := Config{TTS: TTSConfig{
		AutoOrder: []string{"google_chirp", "google_wavenet"},
		MonthlyLimits: map[string]int{
			"google_chirp":   10,
			"google_wavenet": 100,
		},
		GoogleRelay: GoogleRelayTTS{
			Endpoint:    "https://relay.example/v1/tts",
			RelaySecret: "secret",
		},
	}}
	app := &App{cfg: cfg, usage: usage}

	chosen, err := app.chooseProvider("auto", 5)
	if err != nil {
		t.Fatalf("chooseProvider returned error: %v", err)
	}
	if chosen != "google_chirp" {
		t.Fatalf("expected google_chirp, got %s", chosen)
	}

	month := usage.Month()
	usage.Data["google_chirp"] = map[string]int{month: 9}
	chosen, err = app.chooseProvider("auto", 2)
	if err != nil {
		t.Fatalf("chooseProvider fallback returned error: %v", err)
	}
	if chosen != "google_wavenet" {
		t.Fatalf("expected google_wavenet fallback, got %s", chosen)
	}
}

func TestCallGoogleRelayTTSSignsRequestAndReturnsAudio(t *testing.T) {
	const secret = "relay-secret"
	var sawProvider string
	var sawLanguage string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/tts" {
			t.Fatalf("expected /v1/tts, got %s", r.URL.Path)
		}
		body := readAllForTest(t, r)
		ts := r.Header.Get("X-Say-Timestamp")
		if ts == "" {
			t.Fatal("missing X-Say-Timestamp")
		}
		if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
			t.Fatalf("timestamp is not an int: %v", err)
		}
		expected := hmacHexForTest(secret, ts+"."+string(body))
		if got := r.Header.Get("X-Say-Signature"); got != expected {
			t.Fatalf("bad signature: got %s want %s", got, expected)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("bad json payload: %v", err)
		}
		sawProvider, _ = payload["tier"].(string)
		sawLanguage, _ = payload["languageCode"].(string)
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("X-Say-Voice", "en-US-Chirp3-HD-Charon")
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	defer server.Close()

	app := &App{cfg: Config{TTS: TTSConfig{GoogleRelay: GoogleRelayTTS{
		Endpoint:    server.URL + "/v1/tts",
		RelaySecret: secret,
		Timeout:     5,
	}}}}
	audio, contentType, voice, err := app.callGoogleRelayTTS(ttsRequest{
		Text:     "hello",
		Language: "en-US",
		Speed:    0.9,
	}, "google_chirp")
	if err != nil {
		t.Fatalf("callGoogleRelayTTS returned error: %v", err)
	}
	if string(audio) != "mp3-bytes" {
		t.Fatalf("unexpected audio bytes: %q", string(audio))
	}
	if contentType != "audio/mpeg" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	if voice != "en-US-Chirp3-HD-Charon" {
		t.Fatalf("unexpected voice: %s", voice)
	}
	if sawProvider != "chirp3-hd" {
		t.Fatalf("expected chirp3-hd tier, got %s", sawProvider)
	}
	if sawLanguage != "en-US" {
		t.Fatalf("expected en-US language, got %s", sawLanguage)
	}
}

func TestStatusShowsCurrentTTSOnlyAndOmitsBrowserAndTencent(t *testing.T) {
	app := &App{
		cfg: Config{TTS: TTSConfig{
			AutoOrder: []string{"google_chirp", "google_wavenet", "custom"},
			MonthlyLimits: map[string]int{
				"google_chirp": 800000,
			},
			Labels: map[string]string{
				"google_chirp": "Google Chirp 3 HD",
			},
			GoogleRelay: GoogleRelayTTS{
				Endpoint:    "https://relay.example/v1/tts",
				RelaySecret: "secret",
			},
		}},
		usage: &UsageStore{Data: map[string]map[string]int{}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	app.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "browser") || strings.Contains(body, "tencent") {
		t.Fatalf("status leaked removed providers: %s", body)
	}
	var parsed struct {
		CurrentProvider struct {
			Name  string `json:"name"`
			Label string `json:"label"`
			Limit int    `json:"limit"`
		} `json:"current_provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("bad status json: %v", err)
	}
	if parsed.CurrentProvider.Name != "google_chirp" {
		t.Fatalf("current provider = %q", parsed.CurrentProvider.Name)
	}
	if parsed.CurrentProvider.Label != "Google Chirp 3 HD" {
		t.Fatalf("current provider label = %q", parsed.CurrentProvider.Label)
	}
	if parsed.CurrentProvider.Limit != 800000 {
		t.Fatalf("current provider limit = %d", parsed.CurrentProvider.Limit)
	}
}

func TestGuessTTSLanguageUsesHighConfidenceScriptDetection(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "english fallback", text: "skill versus scale", want: "en-US"},
		{name: "mandarin", text: "你好，今天练习发音。", want: "cmn-CN"},
		{name: "japanese kana", text: "こんにちは、発音を練習します。", want: "ja-JP"},
		{name: "korean hangul", text: "안녕하세요 발음을 연습합니다", want: "ko-KR"},
		{name: "russian cyrillic", text: "Привет, я тренирую произношение.", want: "ru-RU"},
		{name: "arabic script", text: "مرحبا كيف حالك", want: "ar-XA"},
		{name: "hindi devanagari", text: "नमस्ते, मैं उच्चारण का अभ्यास कर रहा हूँ।", want: "hi-IN"},
		{name: "bengali script", text: "আমি উচ্চারণ অনুশীলন করছি।", want: "bn-IN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := guessTTSLanguage(tt.text); got != tt.want {
				t.Fatalf("guessTTSLanguage(%q) = %s, want %s", tt.text, got, tt.want)
			}
		})
	}
}

func TestCallGoogleRelayTTSAutoLanguageSendsMandarinCode(t *testing.T) {
	const secret = "relay-secret"
	var sawLanguage string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllForTest(t, r)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("bad json payload: %v", err)
		}
		sawLanguage, _ = payload["languageCode"].(string)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	defer server.Close()

	app := &App{cfg: Config{TTS: TTSConfig{GoogleRelay: GoogleRelayTTS{
		Endpoint:    server.URL + "/v1/tts",
		RelaySecret: secret,
		Timeout:     5,
	}}}}
	if _, _, _, err := app.callGoogleRelayTTS(ttsRequest{Text: "你好，世界"}, "google_chirp"); err != nil {
		t.Fatalf("callGoogleRelayTTS returned error: %v", err)
	}
	if sawLanguage != "cmn-CN" {
		t.Fatalf("expected cmn-CN for auto Mandarin, got %s", sawLanguage)
	}
}

func TestAnalyzeIncludesTranslationLanguageInLLMPrompt(t *testing.T) {
	var sawUserPrompt string
	var sawSystemPrompt string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllForTest(t, r)
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("bad llm payload: %v", err)
		}
		for _, message := range payload.Messages {
			switch message.Role {
			case "system":
				sawSystemPrompt = message.Content
			case "user":
				sawUserPrompt = message.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"explanation_markdown\":\"ok\",\"tts_script\":\"Hello.\",\"tts_script_translation\":\"Hello.\"}"}}]}`))
	}))
	defer server.Close()

	app := &App{
		cfg: Config{LLM: LLMConfig{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "test-model",
			Timeout: 5,
		}},
	}
	reqBody := bytes.NewBufferString(`{"query":"skill vs scale","translation_language":"English"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/analyze", reqBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(sawSystemPrompt, "tts_script is the text that will be spoken by TTS") {
		t.Fatalf("system prompt does not define tts_script responsibility: %s", sawSystemPrompt)
	}
	if !strings.Contains(sawSystemPrompt, "tts_script_translation is the side-by-side reference") {
		t.Fatalf("system prompt does not define translation responsibility: %s", sawSystemPrompt)
	}
	if !strings.Contains(sawSystemPrompt, "selected reference language: English") {
		t.Fatalf("system prompt did not include selected reference language: %s", sawSystemPrompt)
	}
	if !strings.Contains(sawSystemPrompt, "must not change the language of tts_script") {
		t.Fatalf("system prompt does not keep script language separate from reference language: %s", sawSystemPrompt)
	}
	if strings.Contains(sawSystemPrompt, "skill") || strings.Contains(sawSystemPrompt, "Cloud Code") {
		t.Fatalf("system prompt should not contain example-specific rules: %s", sawSystemPrompt)
	}
	if !strings.Contains(sawUserPrompt, "我要学习下面内容的发音：\nskill vs scale") {
		t.Fatalf("user prompt did not include learning phrase before query: %s", sawUserPrompt)
	}
	if !strings.Contains(sawUserPrompt, "目标发音语言或口音偏好：") {
		t.Fatalf("user prompt did not include target pronunciation preference: %s", sawUserPrompt)
	}
	if !strings.Contains(sawUserPrompt, "右侧对照语言：English") {
		t.Fatalf("user prompt did not include side-by-side reference language: %s", sawUserPrompt)
	}
	if strings.Contains(sawUserPrompt, "如果这里是 English") || strings.Contains(sawUserPrompt, "跟读稿翻译语言") {
		t.Fatalf("user prompt still contains patch-style language: %s", sawUserPrompt)
	}
}

func TestHandleConfigMasksSecretsAndPreservesThemOnSave(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	app := &App{
		cfg: Config{
			LLM: LLMConfig{
				BaseURL: "https://api.example.com/v1",
				APIKey:  "llm-secret",
				Model:   "test-model",
				Timeout: 30,
			},
			TTS: TTSConfig{
				DefaultProvider: "auto",
				AutoOrder:       []string{"google_chirp", "custom"},
				MonthlyLimits: map[string]int{
					"google_chirp": 800000,
					"custom":       800000,
				},
				GoogleRelay: GoogleRelayTTS{
					Endpoint:    "https://relay.example/v1/tts",
					RelaySecret: "relay-secret",
					Timeout:     60,
				},
				Custom: CustomTTS{
					BaseURL:        "https://tts.example.com/v1",
					APIKey:         "custom-secret",
					Model:          "tts-model",
					Voice:          "tts-voice",
					ResponseFormat: "mp3",
					Speed:          1,
					Timeout:        60,
				},
			},
		},
		configPath:     configPath,
		configSource:   "local",
		configWritable: true,
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	getRec := httptest.NewRecorder()
	app.handleConfig(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/config code = %d body = %s", getRec.Code, getRec.Body.String())
	}
	var getResp configResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("bad config response: %v", err)
	}
	if getResp.Config.LLM.APIKey != "" || getResp.Config.TTS.GoogleRelay.RelaySecret != "" || getResp.Config.TTS.Custom.APIKey != "" {
		t.Fatalf("config response leaked secrets: %+v", getResp.Config)
	}
	if !getResp.Secrets.LLMAPIKey || !getResp.Secrets.GoogleRelaySecret || !getResp.Secrets.CustomAPIKey {
		t.Fatalf("secret status did not report configured secrets: %+v", getResp.Secrets)
	}

	next := getResp.Config
	next.LLM.Model = "updated-model"
	body, _ := json.Marshal(updateConfigRequest{Config: next})
	putReq := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	app.handleConfig(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config code = %d body = %s", putRec.Code, putRec.Body.String())
	}
	if app.cfg.LLM.Model != "updated-model" {
		t.Fatalf("model was not updated: %s", app.cfg.LLM.Model)
	}
	if app.cfg.LLM.APIKey != "llm-secret" || app.cfg.TTS.GoogleRelay.RelaySecret != "relay-secret" || app.cfg.TTS.Custom.APIKey != "custom-secret" {
		t.Fatalf("existing secrets were not preserved: %+v", app.cfg)
	}
}

func TestDefaultConfigDoesNotEnableCustomTTS(t *testing.T) {
	loaded, err := loadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if providerInOrder(loaded.Config.TTS.AutoOrder, "custom") {
		t.Fatalf("custom should not be in default auto order: %v", loaded.Config.TTS.AutoOrder)
	}
	if _, ok := loaded.Config.TTS.MonthlyLimits["custom"]; ok {
		t.Fatalf("custom should not have a default monthly limit: %+v", loaded.Config.TTS.MonthlyLimits)
	}
	if loaded.Config.TTS.Custom.APIKey != "" {
		t.Fatalf("custom should not inherit an API key")
	}
}

func TestCustomTTSMustBeConfiguredExplicitly(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	config := `{
		"llm": {
			"base_url": "https://api.example.com/v1",
			"model": "model",
			"api_key": "llm-key",
			"timeout": 30
			},
			"tts": {
				"default_provider": "auto",
				"auto_order": ["google_chirp", "google_wavenet", "custom"],
				"custom": {
					"base_url": "https://tts.example.com/v1",
					"api_key": "custom-key",
					"model": "tts-model",
					"voice": "tts-voice"
				}
			}
		}`
	if err := os.WriteFile(configPath, []byte(config), 0640); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if loaded.Config.TTS.Custom.APIKey != "custom-key" {
		t.Fatalf("custom TTS API key was not loaded: %q", loaded.Config.TTS.Custom.APIKey)
	}
	if loaded.Config.TTS.Custom.APIKey == loaded.Config.LLM.APIKey {
		t.Fatalf("custom TTS should not inherit the LLM key")
	}
}

func readAllForTest(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func hmacHexForTest(secret, value string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
