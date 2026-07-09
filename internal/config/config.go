package config

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	DefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type Config struct {
	Environment string
	StateDir    string
	OAuth       OAuthConfig
	Codex       CodexConfig
	HTTP        HTTPConfig
	Security    SecurityConfig
}

type OAuthConfig struct {
	ClientID      string
	AuthBaseURL   string
	AuthorizePath string
	TokenPath     string
	CallbackHost  string
	CallbackPort  int
	CallbackPath  string
	Scope         string
}

type CodexConfig struct {
	BaseURL             string
	ResponsesPath       string
	RequestTimeoutMs    int
	MaxRetries          int
	MaxIdleConns        int
	MaxIdleConnsPerHost int
}

type HTTPConfig struct {
	BindAddr            string
	Port                int
	ReadHeaderTimeoutMs int
	ReadTimeoutMs       int
	WriteTimeoutMs      int
	IdleTimeoutMs       int
	MaxBodyBytes        int64
}

type SecurityConfig struct {
	EncryptTokensAtRest  bool
	RedactLogs           bool
	AllowNonLoopbackBind bool
	AllowedAuthHosts     []string
	AllowedCodexHosts    []string
	SecretKey            string
	MiddlewareToken      string
}

func LoadConfig(ctx context.Context, env map[string]string) (*Config, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	if err := rejectDotEnvFiles(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado durante boot", http.StatusRequestTimeout, ctx.Err())
	default:
	}

	httpPort := getInt(env, "HTTP_PORT", 18787)
	cfg := &Config{
		Environment: get(env, "NODE_ENV", "development"),
		StateDir:    get(env, "MIDDLEWARE_STATE_DIR", ".middleware-state"),
		OAuth: OAuthConfig{
			ClientID:      get(env, "OPENAI_OAUTH_CLIENT_ID", DefaultClientID),
			AuthBaseURL:   get(env, "OPENAI_AUTH_BASE_URL", "https://auth.openai.com"),
			AuthorizePath: get(env, "OPENAI_AUTH_AUTHORIZE_PATH", "/oauth/authorize"),
			TokenPath:     get(env, "OPENAI_AUTH_TOKEN_PATH", "/oauth/token"),
			CallbackHost:  get(env, "OAUTH_CALLBACK_HOST", "localhost"),
			CallbackPort:  getInt(env, "OAUTH_CALLBACK_PORT", httpPort),
			CallbackPath:  get(env, "OAUTH_CALLBACK_PATH", "/v1/auth/openai/callback"),
			Scope:         get(env, "OPENAI_OAUTH_SCOPE", "openid profile email offline_access"),
		},
		Codex: CodexConfig{
			BaseURL:             get(env, "OPENAI_CODEX_BASE_URL", "https://chatgpt.com/backend-api"),
			ResponsesPath:       get(env, "OPENAI_CODEX_RESPONSES_PATH", "/codex/responses"),
			RequestTimeoutMs:    getInt(env, "OPENAI_CODEX_REQUEST_TIMEOUT_MS", 30000),
			MaxRetries:          getInt(env, "OPENAI_CODEX_MAX_RETRIES", 3),
			MaxIdleConns:        getInt(env, "OPENAI_CODEX_MAX_IDLE_CONNS", 100),
			MaxIdleConnsPerHost: getInt(env, "OPENAI_CODEX_MAX_IDLE_CONNS_PER_HOST", 20),
		},
		HTTP: HTTPConfig{
			BindAddr:            get(env, "HTTP_BIND_ADDR", "127.0.0.1"),
			Port:                httpPort,
			ReadHeaderTimeoutMs: getInt(env, "HTTP_READ_HEADER_TIMEOUT_MS", 5000),
			ReadTimeoutMs:       getInt(env, "HTTP_READ_TIMEOUT_MS", 60000),
			WriteTimeoutMs:      getInt(env, "HTTP_WRITE_TIMEOUT_MS", 60000),
			IdleTimeoutMs:       getInt(env, "HTTP_IDLE_TIMEOUT_MS", 120000),
			MaxBodyBytes:        int64(getInt(env, "HTTP_MAX_BODY_BYTES", 2097152)),
		},
		Security: SecurityConfig{
			EncryptTokensAtRest:  getBool(env, "MIDDLEWARE_ENCRYPT_TOKENS_AT_REST", true),
			RedactLogs:           getBool(env, "MIDDLEWARE_REDACT_LOGS", true),
			AllowNonLoopbackBind: getBool(env, "MIDDLEWARE_ALLOW_NON_LOOPBACK_BIND", false),
			AllowedAuthHosts:     splitCSV(get(env, "MIDDLEWARE_ALLOWED_AUTH_HOSTS", "auth.openai.com")),
			AllowedCodexHosts:    splitCSV(get(env, "MIDDLEWARE_ALLOWED_CODEX_HOSTS", "chatgpt.com")),
			SecretKey:            get(env, "MIDDLEWARE_SECRET_KEY", ""),
			MiddlewareToken:      get(env, "MIDDLEWARE_CLIENT_TOKEN", ""),
		},
	}

	if err := cfg.validate(ctx); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (cfg *Config) validate(ctx context.Context) error {
	if cfg.Environment != "development" && cfg.Environment != "test" && cfg.Environment != "production" {
		cfg.Environment = "development"
	}
	if cfg.Security.SecretKey == "" && cfg.Environment == "test" {
		cfg.Security.SecretKey = "dev-only-change-me-dev-only-change-me"
	}
	if cfg.Security.MiddlewareToken == "" && cfg.Environment == "test" {
		cfg.Security.MiddlewareToken = "dev-middleware-token-for-tests-only"
	}
	cfg.Security.SecretKey = strings.TrimSpace(cfg.Security.SecretKey)
	cfg.Security.MiddlewareToken = strings.TrimSpace(cfg.Security.MiddlewareToken)
	if len(cfg.Security.SecretKey) < 32 {
		return security.NewError("ERR_SECRET_KEY_REQUIRED", "MIDDLEWARE_SECRET_KEY precisa ter 32+ caracteres", http.StatusBadRequest)
	}
	if len(cfg.Security.MiddlewareToken) < 32 {
		return security.NewError("ERR_CLIENT_TOKEN_REQUIRED", "MIDDLEWARE_CLIENT_TOKEN precisa ter 32+ caracteres", http.StatusBadRequest)
	}
	if !cfg.Security.RedactLogs {
		return security.NewError("ERR_LOG_REDACTION_REQUIRED", "MIDDLEWARE_REDACT_LOGS nao pode ser desabilitado", http.StatusBadRequest)
	}
	if !isPort(cfg.OAuth.CallbackPort) || !isPort(cfg.HTTP.Port) {
		return security.NewError("ERR_INVALID_PORT", "porta fora do intervalo 1..65535", http.StatusBadRequest)
	}
	if cfg.OAuth.CallbackPort != cfg.HTTP.Port {
		return security.NewError("ERR_OAUTH_CALLBACK_MISMATCH", "OAUTH_CALLBACK_PORT precisa ser igual a HTTP_PORT no servidor unico", http.StatusBadRequest)
	}
	if err := validateRuntimeLimits(cfg); err != nil {
		return err
	}
	if cfg.HTTP.BindAddr == "" {
		cfg.HTTP.BindAddr = "127.0.0.1"
	}
	if !cfg.Security.AllowNonLoopbackBind && !isLoopbackHost(cfg.HTTP.BindAddr) {
		return security.NewError("ERR_HOST_NOT_ALLOWED", "HTTP_BIND_ADDR precisa ser loopback", http.StatusBadRequest)
	}
	if !isLoopbackHost(cfg.OAuth.CallbackHost) {
		return security.NewError("ERR_HOST_NOT_ALLOWED", "callback OAuth local precisa usar host loopback", http.StatusBadRequest)
	}
	if !strings.HasPrefix(cfg.OAuth.CallbackPath, "/") || len(cfg.OAuth.CallbackPath) > 120 {
		return security.WithDetail(security.NewError("ERR_HOST_NOT_ALLOWED", "callback path invalido", http.StatusBadRequest), "OAUTH_CALLBACK_PATH", "precisa iniciar com / e ter ate 120 caracteres")
	}
	if err := security.ValidateAllowedURL(cfg.OAuth.AuthBaseURL, cfg.Security.AllowedAuthHosts); err != nil {
		return err
	}
	if err := security.ValidateAllowedURL(cfg.Codex.BaseURL, cfg.Security.AllowedCodexHosts); err != nil {
		return err
	}
	if err := ensureHTTPURL(cfg.OAuth.AuthBaseURL); err != nil {
		return err
	}
	if err := ensureHTTPURL(cfg.Codex.BaseURL); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado durante boot", http.StatusRequestTimeout, ctx.Err())
	default:
	}
	stateDir, err := secureStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	cfg.StateDir = stateDir
	return nil
}

func secureStateDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", security.Wrap("ERR_STATE_DIR_UNAVAILABLE", "state dir invalido", http.StatusInternalServerError, err)
	}
	if abs == string(filepath.Separator) {
		return "", security.NewError("ERR_STATE_DIR_UNAVAILABLE", "state dir nao pode ser a raiz do sistema", http.StatusBadRequest)
	}
	if cwd, err := os.Getwd(); err == nil && abs == cwd {
		return "", security.NewError("ERR_STATE_DIR_UNAVAILABLE", "state dir precisa ser um diretorio dedicado", http.StatusBadRequest)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", security.Wrap("ERR_STATE_DIR_UNAVAILABLE", "state dir indisponivel", http.StatusInternalServerError, err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", security.Wrap("ERR_STATE_DIR_UNAVAILABLE", "nao foi possivel inspecionar state dir", http.StatusInternalServerError, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", security.NewError("ERR_STATE_DIR_UNAVAILABLE", "state dir precisa ser um diretorio real, sem link simbolico", http.StatusBadRequest)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return "", security.Wrap("ERR_STATE_DIR_UNAVAILABLE", "nao foi possivel ajustar permissao do state dir", http.StatusInternalServerError, err)
	}
	return abs, nil
}

func NewHTTPClient(cfg CodexConfig) *http.Client {
	return &http.Client{
		Timeout: time.Duration(cfg.RequestTimeoutMs) * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          cfg.MaxIdleConns,
			MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

func NewHTTPServer(cfg HTTPConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeoutMs) * time.Millisecond,
		ReadTimeout:       time.Duration(cfg.ReadTimeoutMs) * time.Millisecond,
		WriteTimeout:      time.Duration(cfg.WriteTimeoutMs) * time.Millisecond,
		IdleTimeout:       time.Duration(cfg.IdleTimeoutMs) * time.Millisecond,
		MaxHeaderBytes:    64 << 10,
	}
}

func EnvironMap() map[string]string {
	out := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func TokenURL(cfg OAuthConfig) string {
	base := strings.TrimRight(cfg.AuthBaseURL, "/")
	return base + cfg.TokenPath
}

func AuthorizeURL(cfg OAuthConfig) string {
	base := strings.TrimRight(cfg.AuthBaseURL, "/")
	return base + cfg.AuthorizePath
}

func RedirectURI(cfg OAuthConfig) string {
	u := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(cfg.CallbackHost, strconv.Itoa(cfg.CallbackPort)),
		Path:   cfg.CallbackPath,
	}
	return u.String()
}

func get(env map[string]string, key, fallback string) string {
	if value, ok := env[key]; ok && value != "" {
		return value
	}
	return fallback
}

func getInt(env map[string]string, key string, fallback int) int {
	value := get(env, key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getBool(env map[string]string, key string, fallback bool) bool {
	value := strings.ToLower(get(env, key, ""))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes"
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

func rejectDotEnvFiles() error {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".env" || name == ".envrc" || strings.HasPrefix(name, ".env.") {
			return security.WithDetail(security.NewError("ERR_ENV_FILE_FORBIDDEN", ".env nao e permitido neste middleware", http.StatusBadRequest), "file", name)
		}
	}
	return nil
}

func isPort(port int) bool {
	return port >= 1 && port <= 65535
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func ensureHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return security.NewError("ERR_HOST_NOT_ALLOWED", "URL externa invalida", http.StatusBadRequest)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return security.NewError("ERR_HOST_NOT_ALLOWED", "URL externa precisa ser http ou https", http.StatusBadRequest)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasSuffix(parsed.Host, ":") {
		return security.NewError("ERR_HOST_NOT_ALLOWED", "URL externa nao pode conter userinfo, query ou fragmento", http.StatusBadRequest)
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return security.NewError("ERR_HOST_NOT_ALLOWED", "porta da URL externa invalida", http.StatusBadRequest)
		}
	}
	return nil
}

func validateRuntimeLimits(cfg *Config) error {
	if !inRange(cfg.HTTP.ReadHeaderTimeoutMs, 1000, 60000) ||
		!inRange(cfg.HTTP.ReadTimeoutMs, 1000, 300000) ||
		!inRange(cfg.HTTP.WriteTimeoutMs, 1000, 300000) ||
		!inRange(cfg.HTTP.IdleTimeoutMs, 1000, 600000) ||
		cfg.HTTP.MaxBodyBytes < 1024 || cfg.HTTP.MaxBodyBytes > 10<<20 {
		return security.NewError("ERR_HTTP_CONFIG_INVALID", "timeouts HTTP ou limite de payload invalidos", http.StatusBadRequest)
	}
	if !inRange(cfg.Codex.RequestTimeoutMs, 1000, 300000) ||
		!inRange(cfg.Codex.MaxRetries, 0, 10) ||
		!inRange(cfg.Codex.MaxIdleConns, 1, 1000) ||
		!inRange(cfg.Codex.MaxIdleConnsPerHost, 1, 1000) {
		return security.NewError("ERR_CODEX_CONFIG_INVALID", "timeouts ou pool Codex invalidos", http.StatusBadRequest)
	}
	return nil
}

func inRange(value, min, max int) bool {
	return value >= min && value <= max
}
