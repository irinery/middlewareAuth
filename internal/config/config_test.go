package config

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/irinery/middlewareAuth/internal/security"
)

func testEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token-32-characters",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	env := testEnv(t)
	cfg, err := LoadConfig(context.Background(), env)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.OAuth.CallbackHost != "localhost" {
		t.Fatalf("CallbackHost = %q", cfg.OAuth.CallbackHost)
	}
	if cfg.OAuth.CallbackPort != 18787 {
		t.Fatalf("CallbackPort = %d", cfg.OAuth.CallbackPort)
	}
	if cfg.OAuth.CallbackPath != "/v1/auth/openai/callback" {
		t.Fatalf("CallbackPath = %q", cfg.OAuth.CallbackPath)
	}
	if cfg.HTTP.Port != 18787 {
		t.Fatalf("HTTP.Port = %d", cfg.HTTP.Port)
	}
	if cfg.HTTP.BindAddr != "127.0.0.1" {
		t.Fatalf("HTTP.BindAddr = %q", cfg.HTTP.BindAddr)
	}
	if cfg.Security.AllowedAuthHosts[0] != "auth.openai.com" {
		t.Fatalf("AllowedAuthHosts = %#v", cfg.Security.AllowedAuthHosts)
	}
	server := NewHTTPServer(cfg.HTTP, http.NewServeMux())
	if server.Addr != "127.0.0.1:18787" {
		t.Fatalf("server.Addr = %q", server.Addr)
	}
}

func TestLoadConfigCallbackPortFollowsHTTPPort(t *testing.T) {
	env := testEnv(t)
	env["HTTP_PORT"] = "19090"
	cfg, err := LoadConfig(context.Background(), env)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.OAuth.CallbackPort != 19090 {
		t.Fatalf("CallbackPort = %d", cfg.OAuth.CallbackPort)
	}
}

func TestLoadConfigRejectsAuthHostOutsideAllowlist(t *testing.T) {
	env := testEnv(t)
	env["OPENAI_AUTH_BASE_URL"] = "https://evil.example.com"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_HOST_NOT_ALLOWED" {
		t.Fatalf("error code = %s, want ERR_HOST_NOT_ALLOWED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigCreatesStateDir(t *testing.T) {
	stateDir := t.TempDir() + "/missing/state"
	env := testEnv(t)
	env["MIDDLEWARE_STATE_DIR"] = stateDir
	if _, err := LoadConfig(context.Background(), env); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	info, err := os.Stat(stateDir)
	if err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("state path is not dir")
	}
}

func TestLoadConfigRejectsSymbolicLinkStateDir(t *testing.T) {
	target := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(target, stateDir); err != nil {
		t.Skipf("symlink indisponivel: %v", err)
	}
	env := testEnv(t)
	env["MIDDLEWARE_STATE_DIR"] = stateDir
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_STATE_DIR_UNAVAILABLE" {
		t.Fatalf("error code = %s, want ERR_STATE_DIR_UNAVAILABLE (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRequiresSecretInProduction(t *testing.T) {
	env := testEnv(t)
	delete(env, "MIDDLEWARE_SECRET_KEY")
	env["NODE_ENV"] = "production"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_SECRET_KEY_REQUIRED" {
		t.Fatalf("error code = %s, want ERR_SECRET_KEY_REQUIRED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRequiresClientToken(t *testing.T) {
	env := testEnv(t)
	delete(env, "MIDDLEWARE_CLIENT_TOKEN")
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_CLIENT_TOKEN_REQUIRED" {
		t.Fatalf("error code = %s, want ERR_CLIENT_TOKEN_REQUIRED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRejectsShortClientToken(t *testing.T) {
	env := testEnv(t)
	env["MIDDLEWARE_CLIENT_TOKEN"] = "short-token"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_CLIENT_TOKEN_REQUIRED" {
		t.Fatalf("error code = %s, want ERR_CLIENT_TOKEN_REQUIRED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRejectsSeparateCallbackPort(t *testing.T) {
	env := testEnv(t)
	env["OAUTH_CALLBACK_PORT"] = "18788"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_OAUTH_CALLBACK_MISMATCH" {
		t.Fatalf("error code = %s, want ERR_OAUTH_CALLBACK_MISMATCH (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRejectsUnsafeRuntimeLimits(t *testing.T) {
	env := testEnv(t)
	env["HTTP_READ_TIMEOUT_MS"] = "0"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_HTTP_CONFIG_INVALID" {
		t.Fatalf("error code = %s, want ERR_HTTP_CONFIG_INVALID (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRequiresLogRedaction(t *testing.T) {
	env := testEnv(t)
	env["MIDDLEWARE_REDACT_LOGS"] = "false"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_LOG_REDACTION_REQUIRED" {
		t.Fatalf("error code = %s, want ERR_LOG_REDACTION_REQUIRED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigAllowsTestDefaults(t *testing.T) {
	cfg, err := LoadConfig(context.Background(), map[string]string{
		"NODE_ENV":             "test",
		"MIDDLEWARE_STATE_DIR": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Security.MiddlewareToken == "" || cfg.Security.SecretKey == "" {
		t.Fatalf("test defaults not set: %#v", cfg.Security)
	}
}

func TestLoadConfigRejectsNonLoopbackBindByDefault(t *testing.T) {
	env := testEnv(t)
	env["HTTP_BIND_ADDR"] = "0.0.0.0"
	_, err := LoadConfig(context.Background(), env)
	if security.Code(err) != "ERR_HOST_NOT_ALLOWED" {
		t.Fatalf("error code = %s, want ERR_HOST_NOT_ALLOWED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigAllowsExplicitNonLoopbackBind(t *testing.T) {
	env := testEnv(t)
	env["HTTP_BIND_ADDR"] = "0.0.0.0"
	env["MIDDLEWARE_ALLOW_NON_LOOPBACK_BIND"] = "true"
	cfg, err := LoadConfig(context.Background(), env)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	server := NewHTTPServer(cfg.HTTP, http.NewServeMux())
	if server.Addr != "0.0.0.0:18787" {
		t.Fatalf("server.Addr = %q", server.Addr)
	}
}

func TestLoadConfigRequiresSecretInDevelopment(t *testing.T) {
	_, err := LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_CLIENT_TOKEN": "internal-token-with-32-characters",
	})
	if security.Code(err) != "ERR_SECRET_KEY_REQUIRED" {
		t.Fatalf("error code = %s, want ERR_SECRET_KEY_REQUIRED (%v)", security.Code(err), err)
	}
}

func TestLoadConfigRejectsDotEnvFile(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.WriteFile(".env", []byte("MIDDLEWARE_CLIENT_TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token-32-characters",
	})
	if security.Code(err) != "ERR_ENV_FILE_FORBIDDEN" {
		t.Fatalf("error code = %s, want ERR_ENV_FILE_FORBIDDEN (%v)", security.Code(err), err)
	}
}
