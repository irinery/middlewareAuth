package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/oauth"
	"github.com/irinery/middlewareAuth/internal/security"
)

type fakeRefresher struct{}

func (fakeRefresher) ResolveFreshCredential(ctx context.Context, projectID string, profileID string, minTTLms int64) (*auth.StoredOAuthCredential, error) {
	return &auth.StoredOAuthCredential{Access: "access", AccountID: "account-1", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

type fakeCodex struct{}

func (fakeCodex) SendCodexResponse(ctx context.Context, credential auth.StoredOAuthCredential, request codex.CodexResponseRequest, options codex.CodexTransportOptions) (*codex.CodexResponseStream, error) {
	return &codex.CodexResponseStream{Events: []codex.CodexStreamEvent{{Type: "ok"}}}, nil
}

func testHandler(t *testing.T) *Handler {
	t.Helper()
	cfg, err := config.LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewFileStore(*cfg)
	return NewHandler(*cfg, store, fakeRefresher{}, fakeCodex{}, http.DefaultClient)
}

func TestLoginReturnsAuthURLWithoutSecrets(t *testing.T) {
	handler := testHandler(t)
	body := bytes.NewBufferString(`{"profileId":"default","mode":"oauth"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/acme/auth/openai/login", body)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response LoginStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AuthURL == "" || response.LoginSessionID == "" {
		t.Fatalf("login response incomplete: %#v", response)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("Verifier")) || bytes.Contains(rec.Body.Bytes(), []byte("refresh")) {
		t.Fatalf("login response leaked secret: %s", rec.Body.String())
	}
}

func TestLMStudioAPIKeyAndResponses(t *testing.T) {
	apiKey := "local-api-key"
	lmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"ok lmstudio"}}]}`))
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer lmServer.Close()

	handler := testHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/acme/auth/lmstudio/api-key", bytes.NewBufferString(`{"profileId":"default","baseUrl":"`+lmServer.URL+`","apiKey":"`+apiKey+`"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), apiKey) {
		t.Fatalf("api key leaked in response: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/projects/acme/lmstudio/responses?profileId=default", bytes.NewBufferString(`{"model":"model-a","input":[{"role":"user","content":"oi"}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ok lmstudio") {
		t.Fatalf("response = %s", rec.Body.String())
	}
}

func TestProtectedRouteRequiresMiddlewareAuth(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/acme/auth/openai/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response MiddlewareErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "ERR_MIDDLEWARE_UNAUTHORIZED" {
		t.Fatalf("code = %s", response.Error.Code)
	}
}

func TestExpiredCallbackSessionReturnsGone(t *testing.T) {
	handler := testHandler(t)
	flow := oauth.AuthorizationFlow{State: "expired-state", Verifier: "verifier", RedirectURI: "http://localhost/callback"}
	handler.sessions["session"] = &loginSession{
		LoginSessionID: "session",
		ProjectID:      "acme",
		ProfileID:      "default",
		Mode:           "oauth",
		Status:         "pending",
		Flow:           flow,
		ExpiresAt:      time.Now().Add(-time.Minute).UnixMilli(),
	}
	handler.stateIndex[flow.State] = "session"
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/openai/callback?state=expired-state&code=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if security.Code(security.Public(mustDecodeError(t, rec).Error)) != "ERR_LOGIN_SESSION_EXPIRED" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestCallbackWithValidStateSavesProfileAndCompletesSession(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  jwtToken(map[string]any{"sub": "oauth-user-1", "email": "dev@example.com"}),
			"refresh_token": "refresh-token",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	handler := testHandler(t)
	handler.cfg.OAuth.AuthBaseURL = tokenServer.URL
	handler.client = tokenServer.Client()
	flow := oauth.AuthorizationFlow{State: "valid-state", Verifier: "verifier", RedirectURI: "http://localhost:18787/v1/auth/openai/callback"}
	if err := handler.addSession(loginSession{
		LoginSessionID: "session-ok",
		ProjectID:      "acme",
		ProfileID:      "default",
		Mode:           "oauth",
		Status:         "pending",
		Flow:           flow,
		ExpiresAt:      time.Now().Add(time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/openai/callback?state=valid-state&code=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	credential, err := handler.store.LoadAuthProfile(context.Background(), "acme", "default")
	if err != nil {
		t.Fatalf("LoadAuthProfile() error = %v", err)
	}
	if credential.AccountID != "oauth-user-1" {
		t.Fatalf("AccountID = %q", credential.AccountID)
	}
	response, err := handler.loginSessionResponse("acme", "session-ok")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "completed" {
		t.Fatalf("session status = %q", response.Status)
	}
}

func TestLoginSessionStatusMarksExpired(t *testing.T) {
	handler := testHandler(t)
	handler.sessions["expired"] = &loginSession{
		LoginSessionID: "expired",
		ProjectID:      "acme",
		ProfileID:      "default",
		Mode:           "device_code",
		Status:         "pending",
		ExpiresAt:      time.Now().Add(-time.Second).UnixMilli(),
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/acme/auth/openai/login-sessions/expired", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response LoginSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "expired" {
		t.Fatalf("status = %q", response.Status)
	}
}

func TestDeviceCodeFailureMarksSessionFailed(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_authorization_id":"dev-1","user_code":"ABCD","verification_uri":"https://auth.openai.com/codex/device","interval_ms":1,"expires_in_ms":1000}`))
		case "/api/accounts/deviceauth/token":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	handler := testHandler(t)
	handler.cfg.OAuth.AuthBaseURL = authServer.URL
	handler.client = authServer.Client()
	sessionID := startDeviceCodeForTest(t, handler)
	response := waitSessionStatus(t, handler, "acme", sessionID, "failed")
	if response.Error == nil {
		t.Fatalf("expected session error")
	}
}

func TestDeviceCodeSuccessMarksSessionCompleted(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_authorization_id":"dev-1","user_code":"ABCD","verification_uri":"https://auth.openai.com/codex/device","interval_ms":1,"expires_in_ms":1000}`))
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  jwtToken(map[string]any{"sub": "device-user-1"}),
				"refresh_token": "refresh-device",
				"expires_in":    3600,
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer authServer.Close()

	handler := testHandler(t)
	handler.cfg.OAuth.AuthBaseURL = authServer.URL
	handler.client = authServer.Client()
	sessionID := startDeviceCodeForTest(t, handler)
	response := waitSessionStatus(t, handler, "acme", sessionID, "completed")
	if response.CompletedAt == 0 {
		t.Fatalf("completedAt not set")
	}
	credential, err := handler.store.LoadAuthProfile(context.Background(), "acme", "default")
	if err != nil {
		t.Fatalf("LoadAuthProfile() error = %v", err)
	}
	if credential.AccountID != "device-user-1" {
		t.Fatalf("AccountID = %q", credential.AccountID)
	}
}

func TestInvalidProjectIDReturnsBadRequest(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/../secret/auth/openai/status", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func mustDecodeError(t *testing.T, rec *httptest.ResponseRecorder) MiddlewareErrorResponse {
	t.Helper()
	var response MiddlewareErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func startDeviceCodeForTest(t *testing.T, handler *Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/acme/auth/openai/login", bytes.NewBufferString(`{"profileId":"default","mode":"device_code"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response LoginStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.LoginSessionID
}

func waitSessionStatus(t *testing.T, handler *Handler, projectID, sessionID, want string) *LoginSessionResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := handler.loginSessionResponse(projectID, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if response.Status == want {
			return response
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, _ := handler.loginSessionResponse(projectID, sessionID)
	t.Fatalf("session status = %#v, want %s", response, want)
	return nil
}

func jwtToken(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
