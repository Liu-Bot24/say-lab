package main

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

//go:embed static/*
var staticFiles embed.FS

type Config struct {
	Listen   string    `json:"listen"`
	DataFile string    `json:"data_file"`
	LLM      LLMConfig `json:"llm"`
	TTS      TTSConfig `json:"tts"`
}

type LLMConfig struct {
	BaseURL  string `json:"base_url"`
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Timeout  int    `json:"timeout"`
}

type TTSConfig struct {
	DefaultProvider string              `json:"default_provider"`
	AutoOrder       []string            `json:"auto_order"`
	MonthlyLimits   map[string]int      `json:"monthly_limits"`
	Google          GoogleTTS           `json:"google"`
	GoogleRelay     GoogleRelayTTS      `json:"google_relay"`
	Custom          CustomTTS           `json:"custom"`
	Labels          map[string]string   `json:"labels"`
	VoiceHints      map[string][]string `json:"voice_hints"`
}

type GoogleTTS struct {
	ServiceAccountJSON string `json:"service_account_json,omitempty"`
	ProjectID          string `json:"project_id"`
	PrivateKeyID       string `json:"private_key_id"`
	ClientEmail        string `json:"client_email"`
	PrivateKey         string `json:"private_key"`
	TokenURL           string `json:"token_url"`
	TTSURL             string `json:"tts_url"`
	Timeout            int    `json:"timeout"`
}

type GoogleRelayTTS struct {
	Endpoint    string `json:"endpoint"`
	RelaySecret string `json:"relay_secret"`
	Timeout     int    `json:"timeout"`
}

type CustomTTS struct {
	BaseURL        string  `json:"base_url"`
	APIKey         string  `json:"api_key"`
	Model          string  `json:"model"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed"`
	Timeout        int     `json:"timeout"`
}

type App struct {
	cfg            Config
	usage          *UsageStore
	configPath     string
	configSource   string
	configWritable bool
	tokenMu        sync.Mutex
	googleToken    cachedGoogleToken
}

type cachedGoogleToken struct {
	Value     string
	ExpiresAt time.Time
}

type UsageStore struct {
	mu   sync.Mutex
	file string
	Data map[string]map[string]int `json:"data"`
}

type APIError struct {
	Error string `json:"error"`
}

func main() {
	configPath := flag.String("config", "", "path to config.json")
	flag.Parse()

	loaded, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg := loaded.Config
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:5567"
	}
	if cfg.DataFile == "" {
		cfg.DataFile = "data/usage.json"
	}

	usage, err := LoadUsageStore(cfg.DataFile)
	if err != nil {
		log.Fatalf("load usage store: %v", err)
	}

	app := &App{
		cfg:            cfg,
		usage:          usage,
		configPath:     loaded.Path,
		configSource:   loaded.Source,
		configWritable: loaded.Writable,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleStatic)
	mux.HandleFunc("/api/status", app.handleStatus)
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/analyze", app.handleAnalyze)
	mux.HandleFunc("/api/tts", app.handleTTS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequest(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("say pronunciation listening on %s", cfg.Listen)
	log.Fatal(server.ListenAndServe())
}

type LoadedConfig struct {
	Config   Config
	Path     string
	Source   string
	Writable bool
}

func defaultConfig() Config {
	return Config{
		Listen:   "127.0.0.1:5567",
		DataFile: "data/usage.json",
		LLM: LLMConfig{
			BaseURL: "https://api.deepseek.com/v1",
			Model:   "deepseek-chat",
			Timeout: 90,
		},
		TTS: TTSConfig{
			DefaultProvider: "auto",
			AutoOrder:       []string{"google_chirp", "google_wavenet"},
			MonthlyLimits: map[string]int{
				"google_chirp":   800000,
				"google_wavenet": 4000000,
			},
			Labels: map[string]string{
				"google_chirp":   "Google Chirp 3 HD",
				"google_wavenet": "Google WaveNet",
				"custom":         "Custom TTS",
			},
			VoiceHints: map[string][]string{
				"en": {"en-US", "en-GB"},
				"ja": {"ja-JP"},
				"zh": {"zh-CN", "zh-TW"},
			},
			Google: GoogleTTS{
				TokenURL: "https://oauth2.googleapis.com/token",
				TTSURL:   "https://texttospeech.googleapis.com/v1/text:synthesize",
				Timeout:  60,
			},
			GoogleRelay: GoogleRelayTTS{
				Timeout: 60,
			},
			Custom: CustomTTS{
				ResponseFormat: "mp3",
				Speed:          1,
				Timeout:        60,
			},
		},
	}
}

func loadConfig(path string) (LoadedConfig, error) {
	cfg := defaultConfig()
	if path == "" {
		if envPath := os.Getenv("SAY_CONFIG"); envPath != "" {
			path = envPath
		} else {
			path = "config.json"
		}
	}

	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return LoadedConfig{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LoadedConfig{}, err
	}

	applyEnvConfig(&cfg)
	normalizeConfig(&cfg)

	return LoadedConfig{
		Config:   cfg,
		Path:     path,
		Source:   "local",
		Writable: true,
	}, nil
}

func normalizeConfig(cfg *Config) {
	defaults := defaultConfig()
	migrateGoogleServiceAccount(&cfg.TTS.Google)
	if cfg.Listen == "" {
		cfg.Listen = defaults.Listen
	}
	if cfg.DataFile == "" {
		cfg.DataFile = defaults.DataFile
	}
	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = defaults.LLM.BaseURL
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = defaults.LLM.Model
	}
	if cfg.LLM.Timeout == 0 {
		cfg.LLM.Timeout = defaults.LLM.Timeout
	}
	if cfg.TTS.DefaultProvider == "" {
		cfg.TTS.DefaultProvider = defaults.TTS.DefaultProvider
	}
	if len(cfg.TTS.AutoOrder) == 0 {
		cfg.TTS.AutoOrder = defaults.TTS.AutoOrder
	}
	if cfg.TTS.MonthlyLimits == nil {
		cfg.TTS.MonthlyLimits = defaults.TTS.MonthlyLimits
	} else {
		for k, v := range defaults.TTS.MonthlyLimits {
			if _, ok := cfg.TTS.MonthlyLimits[k]; !ok {
				cfg.TTS.MonthlyLimits[k] = v
			}
		}
	}
	if cfg.TTS.Labels == nil {
		cfg.TTS.Labels = defaults.TTS.Labels
	} else {
		for k, v := range defaults.TTS.Labels {
			if cfg.TTS.Labels[k] == "" {
				cfg.TTS.Labels[k] = v
			}
		}
	}
	if cfg.TTS.VoiceHints == nil {
		cfg.TTS.VoiceHints = defaults.TTS.VoiceHints
	}
	if cfg.TTS.GoogleRelay.Endpoint == "" {
		cfg.TTS.GoogleRelay.Endpoint = defaults.TTS.GoogleRelay.Endpoint
	}
	if cfg.TTS.GoogleRelay.Timeout == 0 {
		cfg.TTS.GoogleRelay.Timeout = defaults.TTS.GoogleRelay.Timeout
	}
	if cfg.TTS.Google.TokenURL == "" {
		cfg.TTS.Google.TokenURL = defaults.TTS.Google.TokenURL
	}
	if cfg.TTS.Google.TTSURL == "" {
		cfg.TTS.Google.TTSURL = defaults.TTS.Google.TTSURL
	}
	if cfg.TTS.Google.Timeout == 0 {
		cfg.TTS.Google.Timeout = defaults.TTS.Google.Timeout
	}
	cfg.TTS.Google.ServiceAccountJSON = ""
	if cfg.TTS.Custom.ResponseFormat == "" {
		cfg.TTS.Custom.ResponseFormat = defaults.TTS.Custom.ResponseFormat
	}
	if cfg.TTS.Custom.Speed == 0 {
		cfg.TTS.Custom.Speed = defaults.TTS.Custom.Speed
	}
	if cfg.TTS.Custom.Timeout == 0 {
		cfg.TTS.Custom.Timeout = defaults.TTS.Custom.Timeout
	}
}

func providerInOrder(order []string, provider string) bool {
	for _, item := range order {
		if item == provider {
			return true
		}
	}
	return false
}

func migrateGoogleServiceAccount(cfg *GoogleTTS) {
	if strings.TrimSpace(cfg.ServiceAccountJSON) == "" {
		return
	}
	account, err := parseGoogleServiceAccountJSON(cfg.ServiceAccountJSON)
	if err != nil {
		return
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = account.ProjectID
	}
	if cfg.PrivateKeyID == "" {
		cfg.PrivateKeyID = account.PrivateKeyID
	}
	if cfg.ClientEmail == "" {
		cfg.ClientEmail = account.ClientEmail
	}
	if cfg.PrivateKey == "" {
		cfg.PrivateKey = account.PrivateKey
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = account.TokenURI
	}
}

func applyEnvConfig(cfg *Config) {
	setStringFromEnv(&cfg.LLM.BaseURL, "SAY_LLM_BASE_URL")
	setStringFromEnv(&cfg.LLM.Endpoint, "SAY_LLM_ENDPOINT")
	setStringFromEnv(&cfg.LLM.Model, "SAY_LLM_MODEL")
	setStringFromEnv(&cfg.LLM.APIKey, "SAY_LLM_API_KEY")
	setIntFromEnv(&cfg.LLM.Timeout, "SAY_LLM_TIMEOUT")

	setStringFromEnv(&cfg.TTS.DefaultProvider, "SAY_TTS_DEFAULT_PROVIDER")
	if order := firstEnv("SAY_TTS_AUTO_ORDER"); order != "" {
		cfg.TTS.AutoOrder = splitCSV(order)
	}
	ensureMonthlyLimits(&cfg.TTS)
	setMonthlyLimitFromEnv(cfg, "google_chirp", "SAY_TTS_GOOGLE_CHIRP_MONTHLY_LIMIT")
	setMonthlyLimitFromEnv(cfg, "google_wavenet", "SAY_TTS_GOOGLE_WAVENET_MONTHLY_LIMIT")
	setMonthlyLimitFromEnv(cfg, "custom", "SAY_TTS_CUSTOM_MONTHLY_LIMIT")

	setStringFromEnv(&cfg.TTS.Google.ServiceAccountJSON, "SAY_GOOGLE_SERVICE_ACCOUNT_JSON", "SAY_GOOGLE_TTS_SERVICE_ACCOUNT_JSON")
	setStringFromEnv(&cfg.TTS.Google.ProjectID, "SAY_GOOGLE_PROJECT_ID")
	setStringFromEnv(&cfg.TTS.Google.PrivateKeyID, "SAY_GOOGLE_PRIVATE_KEY_ID")
	setStringFromEnv(&cfg.TTS.Google.ClientEmail, "SAY_GOOGLE_CLIENT_EMAIL")
	setStringFromEnv(&cfg.TTS.Google.PrivateKey, "SAY_GOOGLE_PRIVATE_KEY")
	setStringFromEnv(&cfg.TTS.Google.TokenURL, "SAY_GOOGLE_TOKEN_URL")
	setStringFromEnv(&cfg.TTS.Google.TTSURL, "SAY_GOOGLE_TTS_URL")
	setIntFromEnv(&cfg.TTS.Google.Timeout, "SAY_GOOGLE_TTS_TIMEOUT")

	setStringFromEnv(&cfg.TTS.GoogleRelay.Endpoint, "SAY_GOOGLE_RELAY_ENDPOINT", "SAY_GOOGLE_TTS_RELAY_ENDPOINT")
	setStringFromEnv(&cfg.TTS.GoogleRelay.RelaySecret, "SAY_GOOGLE_RELAY_SECRET", "SAY_GOOGLE_TTS_RELAY_SECRET")
	setIntFromEnv(&cfg.TTS.GoogleRelay.Timeout, "SAY_GOOGLE_RELAY_TIMEOUT", "SAY_GOOGLE_TTS_RELAY_TIMEOUT")

	setStringFromEnv(&cfg.TTS.Custom.BaseURL, "SAY_TTS_CUSTOM_BASE_URL")
	setStringFromEnv(&cfg.TTS.Custom.APIKey, "SAY_TTS_CUSTOM_API_KEY")
	setStringFromEnv(&cfg.TTS.Custom.Model, "SAY_TTS_CUSTOM_MODEL")
	setStringFromEnv(&cfg.TTS.Custom.Voice, "SAY_TTS_CUSTOM_VOICE")
	setStringFromEnv(&cfg.TTS.Custom.ResponseFormat, "SAY_TTS_CUSTOM_RESPONSE_FORMAT")
	setFloatFromEnv(&cfg.TTS.Custom.Speed, "SAY_TTS_CUSTOM_SPEED")
	setIntFromEnv(&cfg.TTS.Custom.Timeout, "SAY_TTS_CUSTOM_TIMEOUT")
}

func ensureMonthlyLimits(tts *TTSConfig) {
	if tts.MonthlyLimits == nil {
		tts.MonthlyLimits = map[string]int{}
	}
}

func setMonthlyLimitFromEnv(cfg *Config, provider string, names ...string) {
	value := firstEnv(names...)
	if value == "" {
		return
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	ensureMonthlyLimits(&cfg.TTS)
	cfg.TTS.MonthlyLimits[provider] = limit
}

func setStringFromEnv(target *string, names ...string) {
	if value := firstEnv(names...); value != "" {
		*target = value
	}
}

func setIntFromEnv(target *int, names ...string) {
	value := firstEnv(names...)
	if value == "" {
		return
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		*target = parsed
	}
}

func setFloatFromEnv(target *float64, names ...string) {
	value := firstEnv(names...)
	if value == "" {
		return
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		*target = parsed
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func LoadUsageStore(file string) (*UsageStore, error) {
	s := &UsageStore{file: file, Data: map[string]map[string]int{}}
	if err := os.MkdirAll(filepath.Dir(file), 0750); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(file)
	if err == nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		if s.Data == nil {
			s.Data = map[string]map[string]int{}
		}
	}
	return s, nil
}

func (s *UsageStore) Month() string {
	return time.Now().Format("2006-01")
}

func (s *UsageStore) Snapshot() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	month := s.Month()
	out := map[string]int{}
	for provider, byMonth := range s.Data {
		out[provider] = byMonth[month]
	}
	return out
}

func (s *UsageStore) Used(provider string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Data[provider][s.Month()]
}

func (s *UsageStore) Add(provider string, chars int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	month := s.Month()
	if s.Data[provider] == nil {
		s.Data[provider] = map[string]int{}
	}
	s.Data[provider][month] += chars
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

func (app *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean(r.URL.Path)
	if clean == "/" {
		clean = "/index.html"
	}
	name := strings.TrimPrefix(clean, "/")
	content, err := fs.ReadFile(staticFiles, "static/"+name)
	if err != nil {
		content, err = fs.ReadFile(staticFiles, "static/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

func (app *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
		return
	}
	type provider struct {
		Name       string `json:"name"`
		Label      string `json:"label"`
		Configured bool   `json:"configured"`
		Used       int    `json:"used"`
		Limit      int    `json:"limit"`
	}
	providers := app.ttsProviders()
	current := app.currentProviderStatus(providers)
	writeJSON(w, http.StatusOK, map[string]any{
		"llm_configured":   app.cfg.LLM.APIKey != "",
		"default":          app.cfg.TTS.DefaultProvider,
		"auto_order":       app.cfg.TTS.AutoOrder,
		"month":            app.usage.Month(),
		"current_provider": current,
		"providers":        providers,
	})
}

type configResponse struct {
	Config  Config             `json:"config"`
	Secrets configSecretStatus `json:"secrets"`
	Source  configSourceInfo   `json:"source"`
}

type configSecretStatus struct {
	LLMAPIKey                bool `json:"llm_api_key"`
	GoogleServiceAccountJSON bool `json:"google_service_account_json"`
	GooglePrivateKey         bool `json:"google_private_key"`
	GoogleRelaySecret        bool `json:"google_relay_secret"`
	CustomAPIKey             bool `json:"custom_api_key"`
}

type configSourceInfo struct {
	Mode     string `json:"mode"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
}

type updateConfigRequest struct {
	Config Config `json:"config"`
}

func (app *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, app.clientConfig())
	case http.MethodPut:
		app.updateConfig(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
	}
}

func (app *App) clientConfig() configResponse {
	cfg := app.cfg
	secrets := configSecretStatus{
		LLMAPIKey:                cfg.LLM.APIKey != "",
		GoogleServiceAccountJSON: cfg.TTS.Google.ServiceAccountJSON != "",
		GooglePrivateKey:         cfg.TTS.Google.PrivateKey != "",
		GoogleRelaySecret:        cfg.TTS.GoogleRelay.RelaySecret != "",
		CustomAPIKey:             cfg.TTS.Custom.APIKey != "",
	}
	cfg.LLM.APIKey = ""
	cfg.TTS.Google.ServiceAccountJSON = ""
	cfg.TTS.Google.PrivateKey = ""
	cfg.TTS.GoogleRelay.RelaySecret = ""
	cfg.TTS.Custom.APIKey = ""
	return configResponse{
		Config:  cfg,
		Secrets: secrets,
		Source: configSourceInfo{
			Mode:     app.configSource,
			Path:     app.configPath,
			Writable: app.configWritable,
		},
	}
}

func (app *App) updateConfig(w http.ResponseWriter, r *http.Request) {
	if !app.configWritable {
		writeJSON(w, http.StatusConflict, APIError{Error: "当前配置不可写，请检查配置文件权限"})
		return
	}
	var req updateConfigRequest
	if err := readJSON(r, &req, 128*1024); err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{Error: err.Error()})
		return
	}
	next := req.Config
	current := app.cfg
	if strings.TrimSpace(next.LLM.APIKey) == "" {
		next.LLM.APIKey = current.LLM.APIKey
	}
	if strings.TrimSpace(next.TTS.Google.ServiceAccountJSON) == "" {
		next.TTS.Google.ServiceAccountJSON = current.TTS.Google.ServiceAccountJSON
	}
	if strings.TrimSpace(next.TTS.Google.PrivateKey) == "" {
		next.TTS.Google.PrivateKey = current.TTS.Google.PrivateKey
	}
	if strings.TrimSpace(next.TTS.GoogleRelay.RelaySecret) == "" {
		next.TTS.GoogleRelay.RelaySecret = current.TTS.GoogleRelay.RelaySecret
	}
	if strings.TrimSpace(next.TTS.Custom.APIKey) == "" {
		next.TTS.Custom.APIKey = current.TTS.Custom.APIKey
	}
	normalizeConfig(&next)
	if err := writeConfigFile(app.configPath, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIError{Error: err.Error()})
		return
	}
	app.cfg = next
	writeJSON(w, http.StatusOK, app.clientConfig())
}

func writeConfigFile(p string, cfg Config) error {
	if p == "" {
		p = "config.json"
	}
	if dir := filepath.Dir(p); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(p, b, 0640)
}

func (app *App) ttsProviders() []struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Used       int    `json:"used"`
	Limit      int    `json:"limit"`
} {
	usage := app.usage.Snapshot()
	limits := app.cfg.TTS.MonthlyLimits
	return []struct {
		Name       string `json:"name"`
		Label      string `json:"label"`
		Configured bool   `json:"configured"`
		Used       int    `json:"used"`
		Limit      int    `json:"limit"`
	}{
		{Name: "google_chirp", Label: labelFor(app.cfg, "google_chirp"), Configured: app.providerConfigured("google_chirp"), Used: usage["google_chirp"], Limit: limits["google_chirp"]},
		{Name: "google_wavenet", Label: labelFor(app.cfg, "google_wavenet"), Configured: app.providerConfigured("google_wavenet"), Used: usage["google_wavenet"], Limit: limits["google_wavenet"]},
		{Name: "custom", Label: labelFor(app.cfg, "custom"), Configured: app.providerConfigured("custom"), Used: usage["custom"], Limit: limits["custom"]},
	}
}

func (app *App) currentProviderStatus(providers []struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Used       int    `json:"used"`
	Limit      int    `json:"limit"`
}) any {
	for _, name := range app.autoProviderOrder() {
		for _, p := range providers {
			if p.Name == name && p.Configured && app.withinLimit(p.Name, 0) {
				return p
			}
		}
	}
	return nil
}

func labelFor(cfg Config, provider string) string {
	if cfg.TTS.Labels != nil && cfg.TTS.Labels[provider] != "" {
		return cfg.TTS.Labels[provider]
	}
	return provider
}

type analyzeRequest struct {
	Query               string `json:"query"`
	Accent              string `json:"accent"`
	Notes               string `json:"notes"`
	TranslationLanguage string `json:"translation_language"`
}

type analyzeResponse struct {
	Summary              string `json:"summary"`
	ExplanationMarkdown  string `json:"explanation_markdown"`
	TTSScript            string `json:"tts_script"`
	TTSScriptTranslation string `json:"tts_script_translation"`
	Raw                  string `json:"raw,omitempty"`
}

func (app *App) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
		return
	}
	var req analyzeRequest
	if err := readJSON(r, &req, 64*1024); err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{Error: err.Error()})
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeJSON(w, http.StatusBadRequest, APIError{Error: "请输入要分析的单词、短语或问题"})
		return
	}
	if countRunes(req.Query) > 4000 {
		writeJSON(w, http.StatusBadRequest, APIError{Error: "输入太长，请控制在 4000 字以内"})
		return
	}
	if app.cfg.LLM.APIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, APIError{Error: "LLM 未配置"})
		return
	}
	resp, err := app.callLLM(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, APIError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (app *App) callLLM(req analyzeRequest) (analyzeResponse, error) {
	accent := strings.TrimSpace(req.Accent)
	if accent == "" {
		accent = "Auto-detect from the user's text; use a standard pronunciation for the detected language unless the user asks for a specific accent"
	}
	translationLanguage := strings.TrimSpace(req.TranslationLanguage)
	if translationLanguage == "" {
		translationLanguage = "Simplified Chinese"
	}
	system := fmt.Sprintf(`You are Say Lab, a precise and practical pronunciation coach for language learners.
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
- tts_script_translation is the side-by-side reference. It must use the selected reference language: %s. It must match tts_script line count and line order, and must not change the language of tts_script.

Return STRICT JSON only, without markdown fences. The JSON schema is:
{
  "summary": "one short Chinese sentence",
  "explanation_markdown": "Markdown explanation in Chinese with IPA, key mouth/tongue/vowel/consonant differences, common ASR confusion reasons, and 3 short practice sentences",
  "tts_script": "A clean script intended to be read aloud by TTS. Use the target pronunciation language from the user's input or stated preference. Include listen-and-repeat pacing, minimal symbols, and short lines.",
  "tts_script_translation": "Line-by-line %s meaning notes or translation for tts_script. Keep the same line count and line order."
}
The tts_script_translation is not read aloud; it is only displayed as a side-by-side reference.`, translationLanguage, translationLanguage)
	user := fmt.Sprintf("我要学习下面内容的发音：\n%s\n\n目标发音语言或口音偏好：\n%s\n\n右侧对照语言：%s\n\n补充说明：\n%s", req.Query, accent, translationLanguage, strings.TrimSpace(req.Notes))
	payload := map[string]any{
		"model": app.cfg.LLM.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
		"max_tokens":  1800,
	}
	b, _ := json.Marshal(payload)
	url := strings.TrimSpace(app.cfg.LLM.Endpoint)
	if url == "" {
		url = strings.TrimRight(app.cfg.LLM.BaseURL, "/") + "/chat/completions"
	}
	client := &http.Client{Timeout: time.Duration(app.cfg.LLM.Timeout) * time.Second}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return analyzeResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+app.cfg.LLM.APIKey)
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return analyzeResponse{}, err
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return analyzeResponse{}, fmt.Errorf("LLM 请求失败：%s %s", httpResp.Status, string(body))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return analyzeResponse{}, err
	}
	if len(parsed.Choices) == 0 {
		return analyzeResponse{}, errors.New("LLM 没有返回结果")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	var out analyzeResponse
	if err := json.Unmarshal([]byte(extractJSONObject(content)), &out); err != nil {
		out = analyzeResponse{
			Summary:              "已生成发音说明。",
			ExplanationMarkdown:  content,
			TTSScript:            fallbackScript(req.Query),
			TTSScriptTranslation: fallbackScriptTranslation(req.Query, translationLanguage),
			Raw:                  content,
		}
	}
	if strings.TrimSpace(out.TTSScript) == "" {
		out.TTSScript = fallbackScript(req.Query)
	}
	if strings.TrimSpace(out.TTSScriptTranslation) == "" {
		out.TTSScriptTranslation = fallbackScriptTranslation(out.TTSScript, translationLanguage)
	}
	return out, nil
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func fallbackScript(q string) string {
	return "Listen and repeat.\n" + strings.TrimSpace(q) + "\nAgain.\n" + strings.TrimSpace(q)
}

func fallbackScriptTranslation(q, translationLanguage string) string {
	if strings.Contains(strings.ToLower(translationLanguage), "english") {
		return "Listen and repeat.\n" + strings.TrimSpace(q) + "\nAgain.\n" + strings.TrimSpace(q)
	}
	return "听并跟读。\n" + strings.TrimSpace(q) + "\n再来一次。\n" + strings.TrimSpace(q)
}

type ttsRequest struct {
	Text     string  `json:"text"`
	Provider string  `json:"provider"`
	Language string  `json:"language"`
	Voice    string  `json:"voice"`
	Speed    float64 `json:"speed"`
}

func (app *App) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
		return
	}
	var req ttsRequest
	if err := readJSON(r, &req, 128*1024); err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{Error: err.Error()})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, APIError{Error: "没有可朗读的文本"})
		return
	}
	if countRunes(req.Text) > 5000 {
		writeJSON(w, http.StatusBadRequest, APIError{Error: "朗读文本太长，请控制在 5000 字以内"})
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = app.cfg.TTS.DefaultProvider
	}
	if provider == "" {
		provider = "auto"
	}
	chosen, err := app.chooseProvider(provider, countRunes(req.Text))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}

	var audio []byte
	var contentType string
	var voiceName string
	switch chosen {
	case "google_chirp", "google_wavenet":
		audio, contentType, voiceName, err = app.callGoogleRelayTTS(req, chosen)
	case "custom":
		audio, contentType, err = app.callCustomTTS(req)
	default:
		err = fmt.Errorf("未知 TTS provider: %s", chosen)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"provider": chosen, "error": err.Error()})
		return
	}
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	if err := app.usage.Add(chosen, countRunes(req.Text)); err != nil {
		log.Printf("usage add failed: %v", err)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Say-Provider", chosen)
	if voiceName != "" {
		w.Header().Set("X-Say-Voice", voiceName)
	}
	w.Header().Set("X-Say-Chars", strconv.Itoa(countRunes(req.Text)))
	_, _ = w.Write(audio)
}

func (app *App) chooseProvider(provider string, chars int) (string, error) {
	if provider != "auto" {
		if !app.providerConfigured(provider) {
			return "", fmt.Errorf("%s 未配置", provider)
		}
		if !app.withinLimit(provider, chars) {
			return "", fmt.Errorf("%s 本月用量已接近限制", provider)
		}
		return provider, nil
	}
	for _, p := range app.autoProviderOrder() {
		if app.providerConfigured(p) && app.withinLimit(p, chars) {
			return p, nil
		}
	}
	return "", errors.New("没有可用的云端 TTS")
}

func (app *App) autoProviderOrder() []string {
	seen := map[string]bool{}
	order := make([]string, 0, len(app.cfg.TTS.AutoOrder)+3)
	for _, provider := range app.cfg.TTS.AutoOrder {
		if provider == "" || seen[provider] {
			continue
		}
		seen[provider] = true
		order = append(order, provider)
	}
	for _, provider := range []string{"google_chirp", "google_wavenet", "custom"} {
		if seen[provider] {
			continue
		}
		seen[provider] = true
		order = append(order, provider)
	}
	return order
}

func (app *App) providerConfigured(provider string) bool {
	switch provider {
	case "google_chirp", "google_wavenet":
		return app.googleConfigured()
	case "custom":
		cfg := app.cfg.TTS.Custom
		return cfg.BaseURL != "" && cfg.APIKey != "" && cfg.Model != "" && cfg.Voice != ""
	default:
		return false
	}
}

func (app *App) googleConfigured() bool {
	relay := app.cfg.TTS.GoogleRelay
	if relay.Endpoint != "" && relay.RelaySecret != "" {
		return true
	}
	_, err := googleServiceAccountFromConfig(app.cfg.TTS.Google)
	return err == nil
}

func (app *App) withinLimit(provider string, chars int) bool {
	limit := app.cfg.TTS.MonthlyLimits[provider]
	if limit <= 0 {
		return true
	}
	return app.usage.Used(provider)+chars <= limit
}

func (app *App) callGoogleRelayTTS(req ttsRequest, provider string) ([]byte, string, string, error) {
	cfg := app.cfg.TTS.GoogleRelay
	if _, err := googleServiceAccountFromConfig(app.cfg.TTS.Google); err == nil {
		return app.callGoogleDirectTTS(req, provider)
	}
	if cfg.Endpoint == "" {
		return nil, "", "", errors.New("Google TTS 未配置")
	}
	if cfg.RelaySecret == "" {
		return nil, "", "", errors.New("Google TTS relay secret 未配置")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = guessTTSLanguage(req.Text)
	}
	tier := "wavenet"
	if provider == "google_chirp" {
		tier = "chirp3-hd"
	}
	payload := map[string]any{
		"text":         req.Text,
		"languageCode": language,
		"tier":         tier,
	}
	if req.Speed > 0 {
		payload["speakingRate"] = req.Speed
	}
	if strings.TrimSpace(req.Voice) != "" && strings.Contains(req.Voice, "Google") {
		payload["voiceName"] = strings.TrimSpace(req.Voice)
	}
	body, _ := json.Marshal(payload)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := hex.EncodeToString(hmacSHA256([]byte(cfg.RelaySecret), timestamp+"."+string(body)))

	httpReq, err := http.NewRequest(http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Say-Timestamp", timestamp)
	httpReq.Header.Set("X-Say-Signature", signature)

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", "", err
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 16<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("Google TTS relay 请求失败：%s %s", httpResp.Status, string(respBody))
	}
	ct := httpResp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return respBody, ct, httpResp.Header.Get("X-Say-Voice"), nil
}

type googleServiceAccount struct {
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	TokenURI     string `json:"token_uri"`
}

func (app *App) callGoogleDirectTTS(req ttsRequest, provider string) ([]byte, string, string, error) {
	cfg := app.cfg.TTS.Google
	account, err := googleServiceAccountFromConfig(cfg)
	if err != nil {
		return nil, "", "", err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = guessTTSLanguage(req.Text)
	}
	tier := "wavenet"
	if provider == "google_chirp" {
		tier = "chirp3-hd"
	}
	voiceName := defaultGoogleVoiceName(language, tier)
	if strings.TrimSpace(req.Voice) != "" && strings.Contains(req.Voice, "Google") {
		voiceName = strings.TrimSpace(req.Voice)
	}

	audioConfig := map[string]any{"audioEncoding": "MP3"}
	if tier != "chirp3-hd" && req.Speed > 0 {
		audioConfig["speakingRate"] = req.Speed
	}
	payload := map[string]any{
		"input": map[string]string{
			"text": req.Text,
		},
		"voice": map[string]string{
			"languageCode": language,
			"name":         voiceName,
		},
		"audioConfig": audioConfig,
	}
	body, _ := json.Marshal(payload)
	token, err := app.googleAccessToken(account, cfg, timeout)
	if err != nil {
		return nil, "", "", err
	}
	ttsURL := strings.TrimSpace(cfg.TTSURL)
	if ttsURL == "" {
		ttsURL = "https://texttospeech.googleapis.com/v1/text:synthesize"
	}
	httpReq, err := http.NewRequest(http.MethodPost, ttsURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", "", err
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 16<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("Google TTS 请求失败：%s %s", httpResp.Status, string(respBody))
	}
	var parsed struct {
		AudioContent string `json:"audioContent"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, "", "", err
	}
	audio, err := base64.StdEncoding.DecodeString(parsed.AudioContent)
	if err != nil {
		return nil, "", "", err
	}
	return audio, "audio/mpeg", voiceName, nil
}

func googleServiceAccountFromConfig(cfg GoogleTTS) (googleServiceAccount, error) {
	account := googleServiceAccount{
		ProjectID:    strings.TrimSpace(cfg.ProjectID),
		PrivateKeyID: strings.TrimSpace(cfg.PrivateKeyID),
		ClientEmail:  strings.TrimSpace(cfg.ClientEmail),
		PrivateKey:   strings.TrimSpace(cfg.PrivateKey),
		TokenURI:     strings.TrimSpace(cfg.TokenURL),
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		parsed, err := parseGoogleServiceAccountJSON(cfg.ServiceAccountJSON)
		if err != nil {
			return account, err
		}
		if account.ProjectID == "" {
			account.ProjectID = parsed.ProjectID
		}
		if account.PrivateKeyID == "" {
			account.PrivateKeyID = parsed.PrivateKeyID
		}
		if account.ClientEmail == "" {
			account.ClientEmail = parsed.ClientEmail
		}
		if account.PrivateKey == "" {
			account.PrivateKey = parsed.PrivateKey
		}
		if account.TokenURI == "" {
			account.TokenURI = parsed.TokenURI
		}
	}
	if strings.TrimSpace(account.ClientEmail) == "" || strings.TrimSpace(account.PrivateKey) == "" {
		return account, errors.New("Google TTS 缺少 client_email 或 private_key")
	}
	if account.TokenURI == "" {
		account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return account, nil
}

func parseGoogleServiceAccountJSON(raw string) (googleServiceAccount, error) {
	var account googleServiceAccount
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &account); err != nil {
		return account, fmt.Errorf("Google service account JSON 无效：%w", err)
	}
	if strings.TrimSpace(account.ClientEmail) == "" || strings.TrimSpace(account.PrivateKey) == "" {
		return account, errors.New("Google service account JSON 缺少 client_email 或 private_key")
	}
	if account.TokenURI == "" {
		account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return account, nil
}

func (app *App) googleAccessToken(account googleServiceAccount, cfg GoogleTTS, timeout int) (string, error) {
	app.tokenMu.Lock()
	defer app.tokenMu.Unlock()

	now := time.Now()
	if app.googleToken.Value != "" && app.googleToken.ExpiresAt.After(now.Add(60*time.Second)) {
		return app.googleToken.Value, nil
	}
	assertion, err := signGoogleJWT(account, now)
	if err != nil {
		return "", err
	}
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if tokenURL == "" {
		tokenURL = account.TokenURI
	}
	account.TokenURI = tokenURL
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	httpReq, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 2<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", fmt.Errorf("Google token 请求失败：%s %s", httpResp.Status, string(body))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.AccessToken == "" {
		return "", errors.New("Google token 响应缺少 access_token")
	}
	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	app.googleToken = cachedGoogleToken{
		Value:     parsed.AccessToken,
		ExpiresAt: now.Add(time.Duration(expiresIn) * time.Second),
	}
	return parsed.AccessToken, nil
}

func signGoogleJWT(account googleServiceAccount, now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claim := map[string]any{
		"iss":   account.ClientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   account.TokenURI,
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
	}
	signingInput := base64URLJSON(header) + "." + base64URLJSON(claim)
	privateKey, err := parseRSAPrivateKey(account.PrivateKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(raw, `\n`, "\n")))
	if block == nil {
		return nil, errors.New("Google private_key 不是有效 PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("Google private_key 不是 RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func base64URLJSON(value any) string {
	b, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(b)
}

func defaultGoogleVoiceName(languageCode, tier string) string {
	if strings.Contains(strings.ToLower(tier), "chirp") {
		voices := map[string]string{
			"ar-XA":  "ar-XA-Chirp3-HD-Charon",
			"bn-IN":  "bn-IN-Chirp3-HD-Charon",
			"cmn-CN": "cmn-CN-Chirp3-HD-Charon",
			"de-DE":  "de-DE-Chirp3-HD-Charon",
			"en-GB":  "en-GB-Chirp3-HD-Charon",
			"en-US":  "en-US-Chirp3-HD-Charon",
			"es-ES":  "es-ES-Chirp3-HD-Charon",
			"fr-FR":  "fr-FR-Chirp3-HD-Charon",
			"hi-IN":  "hi-IN-Chirp3-HD-Charon",
			"ja-JP":  "ja-JP-Chirp3-HD-Charon",
			"ko-KR":  "ko-KR-Chirp3-HD-Charon",
			"pt-BR":  "pt-BR-Chirp3-HD-Charon",
			"ru-RU":  "ru-RU-Chirp3-HD-Charon",
		}
		if voice := voices[languageCode]; voice != "" {
			return voice
		}
		return "en-US-Chirp3-HD-Charon"
	}
	voices := map[string]string{
		"ar-XA":  "ar-XA-Wavenet-B",
		"bn-IN":  "bn-IN-Wavenet-B",
		"cmn-CN": "cmn-CN-Wavenet-A",
		"de-DE":  "de-DE-Wavenet-B",
		"en-GB":  "en-GB-Wavenet-B",
		"en-US":  "en-US-Wavenet-D",
		"es-ES":  "es-ES-Wavenet-B",
		"fr-FR":  "fr-FR-Wavenet-B",
		"hi-IN":  "hi-IN-Wavenet-D",
		"ja-JP":  "ja-JP-Wavenet-B",
		"ko-KR":  "ko-KR-Wavenet-A",
		"pt-BR":  "pt-BR-Wavenet-A",
		"ru-RU":  "ru-RU-Wavenet-A",
	}
	if voice := voices[languageCode]; voice != "" {
		return voice
	}
	return "en-US-Wavenet-D"
}

func guessTTSLanguage(text string) string {
	if hasRuneIn(text, inJapaneseKana) {
		return "ja-JP"
	}
	if hasRuneIn(text, inHangul) {
		return "ko-KR"
	}
	if hasRuneIn(text, inCyrillic) {
		return "ru-RU"
	}
	if hasRuneIn(text, inArabicScript) {
		return "ar-XA"
	}
	if hasRuneIn(text, inDevanagari) {
		return "hi-IN"
	}
	if hasRuneIn(text, inBengali) {
		return "bn-IN"
	}
	if hasRuneIn(text, inHan) {
		return "cmn-CN"
	}
	words := latinWords(text)
	if containsAnyWord(words, "hola", "gracias", "porque", "para", "estoy", "senor", "señor", "manana", "mañana") || strings.ContainsAny(text, "¿¡ñ") {
		return "es-ES"
	}
	if containsAnyWord(words, "bonjour", "merci", "pourquoi", "avec", "francais", "français", "tres", "très") || strings.ContainsAny(text, "àâæçèêëîïôœùûÿ") {
		return "fr-FR"
	}
	if containsAnyWord(words, "guten", "danke", "nicht", "bitte", "deutsch", "sprechen") || strings.ContainsAny(text, "äöüß") {
		return "de-DE"
	}
	if containsAnyWord(words, "ola", "olá", "obrigado", "obrigada", "voce", "você", "estou", "portugues", "português") || strings.ContainsAny(text, "ãõ") {
		return "pt-BR"
	}
	return "en-US"
}

func hasRuneIn(text string, match func(rune) bool) bool {
	for _, r := range text {
		if match(r) {
			return true
		}
	}
	return false
}

func inJapaneseKana(r rune) bool {
	return (r >= '\u3040' && r <= '\u30ff') || (r >= '\uff66' && r <= '\uff9f')
}

func inHangul(r rune) bool {
	return (r >= '\uac00' && r <= '\ud7af') || (r >= '\u1100' && r <= '\u11ff') || (r >= '\u3130' && r <= '\u318f')
}

func inCyrillic(r rune) bool {
	return unicode.IsLetter(r) && r >= '\u0400' && r <= '\u04ff'
}

func inArabicScript(r rune) bool {
	return unicode.IsLetter(r) && ((r >= '\u0600' && r <= '\u06ff') || (r >= '\u0750' && r <= '\u077f') || (r >= '\u08a0' && r <= '\u08ff'))
}

func inDevanagari(r rune) bool {
	return unicode.IsLetter(r) && r >= '\u0900' && r <= '\u097f'
}

func inBengali(r rune) bool {
	return unicode.IsLetter(r) && r >= '\u0980' && r <= '\u09ff'
}

func inHan(r rune) bool {
	return unicode.IsLetter(r) && ((r >= '\u3400' && r <= '\u4dbf') || (r >= '\u4e00' && r <= '\u9fff') || (r >= '\uf900' && r <= '\ufaff'))
}

func latinWords(text string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	seen := make(map[string]bool, len(words))
	for _, word := range words {
		if word != "" {
			seen[word] = true
		}
	}
	return seen
}

func containsAnyWord(words map[string]bool, candidates ...string) bool {
	for _, word := range candidates {
		if words[word] {
			return true
		}
	}
	return false
}

func (app *App) callCustomTTS(req ttsRequest) ([]byte, string, error) {
	cfg := app.cfg.TTS.Custom
	model := cfg.Model
	voice := cfg.Voice
	if strings.TrimSpace(req.Voice) != "" {
		voice = strings.TrimSpace(req.Voice)
	}
	speed := cfg.Speed
	if req.Speed > 0 {
		speed = req.Speed
	}
	if speed <= 0 {
		speed = 1
	}
	format := cfg.ResponseFormat
	if format == "" {
		format = "mp3"
	}
	payload := map[string]any{
		"model":           model,
		"input":           req.Text,
		"voice":           voice,
		"response_format": format,
		"speed":           speed,
	}
	b, _ := json.Marshal(payload)
	client := &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second}
	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/audio/speech", bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 16<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("自定义 TTS 请求失败：%s %s", httpResp.Status, string(body))
	}
	ct := httpResp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return body, ct, nil
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(msg))
	return h.Sum(nil)
}

func readJSON(r *http.Request, v any, limit int64) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return errors.New("request body too large")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty request body")
	}
	return json.Unmarshal(body, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func countRunes(s string) int {
	return len([]rune(s))
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
