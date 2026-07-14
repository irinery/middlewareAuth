package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/security"
)

type recordingCodexSender struct {
	request codex.CodexResponseRequest
}

func (s *recordingCodexSender) SendCodexResponse(_ context.Context, _ auth.StoredOAuthCredential, request codex.CodexResponseRequest, _ codex.CodexTransportOptions) (*codex.CodexResponseStream, error) {
	s.request = request
	return &codex.CodexResponseStream{OutputText: `{"summary":"ok"}`, Usage: codex.CodexUsage{TotalTokens: 12}}, nil
}

type missingCredentialRefresher struct{}

func (missingCredentialRefresher) ResolveFreshCredential(context.Context, string, string, int64) (*auth.StoredOAuthCredential, error) {
	return nil, security.NewError("ERR_AUTH_PROFILE_NOT_FOUND", "perfil ausente", http.StatusNotFound)
}

func TestLLMProvidersExposesCanonicalBlackBoxContract(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/pockettrace/llm/providers", nil)
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response LLMProviderCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ContractVersion != "middlewareauth.llm.v1" || len(response.Providers) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if response.Providers[0].ID != providerOpenAI || response.Providers[1].ID != providerLMStudio {
		t.Fatalf("providers = %#v", response.Providers)
	}
	if !response.Providers[0].Capabilities.Refresh || response.Providers[1].Capabilities.Refresh {
		t.Fatalf("refresh capabilities = %#v", response.Providers)
	}
	if !response.Providers[0].Capabilities.Intelligence || response.Providers[1].Capabilities.Intelligence {
		t.Fatalf("intelligence capabilities = %#v", response.Providers)
	}
	if !response.Providers[0].Capabilities.Store || response.Providers[1].Capabilities.Store {
		t.Fatalf("store capabilities = %#v", response.Providers)
	}
	if len(response.Providers[1].Auth.Fields) != 2 || !response.Providers[1].Auth.Fields[1].Secret {
		t.Fatalf("auth fields = %#v", response.Providers[1].Auth.Fields)
	}
	if stringsContainAny(rec.Body.String(), "accessToken", "refreshToken", `"apiKey":"`) {
		t.Fatalf("catalog leaked credential fields: %s", rec.Body.String())
	}
}

func TestLLMLoginAcceptsCatalogDrivenAuthFieldsWithoutReturningSecret(t *testing.T) {
	const apiKey = "local-secret-value"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("provider request path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	defer provider.Close()

	handler := testHandler(t)
	body, err := json.Marshal(map[string]any{
		"providerId": "lmstudio",
		"profileId":  "work",
		"mode":       "api_key",
		"authFields": map[string]any{"baseUrl": provider.URL, "apiKey": apiKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/pockettrace/llm/login", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if stringsContainAny(rec.Body.String(), apiKey, `"apiKey"`) {
		t.Fatalf("login response leaked API key: %s", rec.Body.String())
	}
	var response LLMLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Authenticated || response.ProviderID != providerLMStudio || response.ModelCount != 1 {
		t.Fatalf("response = %#v", response)
	}
	credential, err := handler.store.LoadProviderCredential(context.Background(), providerLMStudio, "pockettrace", "work")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Access != apiKey {
		t.Fatal("API key was not stored in middleware credential store")
	}
}

func TestLLMResponsesDispatchesInternallyWithoutControlFieldsReachingProvider(t *testing.T) {
	handler := testHandler(t)
	sender := &recordingCodexSender{}
	handler.codex = sender
	body := `{
		"providerId":"openai",
		"profileId":"work",
		"model":"gpt-test",
		"instructions":"system",
		"input":[{"role":"user","content":"oi"}],
		"stream":false,
		"store":false,
		"max_output_tokens":12000
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/pockettrace/llm/responses", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if sender.request.Model != "gpt-test" || len(sender.request.Input) != 1 {
		t.Fatalf("request = %#v", sender.request)
	}
	if _, exists := sender.request.Extra["providerId"]; exists {
		t.Fatalf("providerId leaked into provider payload: %#v", sender.request.Extra)
	}
	if _, exists := sender.request.Extra["profileId"]; exists {
		t.Fatalf("profileId leaked into provider payload: %#v", sender.request.Extra)
	}
	if value, ok := sender.request.Extra["max_output_tokens"].(float64); !ok || value != 12000 {
		t.Fatalf("max_output_tokens = %#v", sender.request.Extra["max_output_tokens"])
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"outputText":"{\"summary\":\"ok\"}"`)) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}

func TestLLMStatusKeepsProjectsIsolated(t *testing.T) {
	handler := testHandler(t)
	_, err := handler.store.SaveAuthProfile(context.Background(), "pockettrace", "default", auth.StoredOAuthCredential{
		Access:    "provider-secret",
		Refresh:   "provider-refresh",
		Expires:   1780000000000,
		AccountID: "account-pockettrace",
	})
	if err != nil {
		t.Fatal(err)
	}

	status := func(projectID string) LLMStatusResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/projects/"+projectID+"/llm/status?providerId=openai&profileId=default", nil)
		req.Header.Set("Authorization", "Bearer "+handlerTestToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("project=%s status=%d body=%s", projectID, rec.Code, rec.Body.String())
		}
		var response LLMStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if stringsContainAny(rec.Body.String(), "provider-secret", "provider-refresh") {
			t.Fatalf("credential leaked: %s", rec.Body.String())
		}
		return response
	}

	pocketTrace := status("pockettrace")
	pocketWiki := status("pocketwiki")
	if !pocketTrace.Authenticated || pocketTrace.AccountID != "account-pockettrace" {
		t.Fatalf("pockettrace status = %#v", pocketTrace)
	}
	if pocketWiki.Authenticated || pocketWiki.AccountID != "" {
		t.Fatalf("pocketwiki status = %#v", pocketWiki)
	}
}

func TestLLMResponsesNormalizesMissingCredentialAndUnknownProvider(t *testing.T) {
	handler := testHandler(t)
	handler.refresher = missingCredentialRefresher{}

	assertError := func(body, wantCode string, wantStatus int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/projects/pockettrace/llm/responses", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+handlerTestToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response MiddlewareErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Code != wantCode {
			t.Fatalf("code = %s body=%s", response.Error.Code, rec.Body.String())
		}
	}

	assertError(`{"providerId":"openai","profileId":"default","model":"gpt-test","input":[{"role":"user","content":"oi"}]}`, "ERR_LLM_AUTH_REQUIRED", http.StatusUnauthorized)
	assertError(`{"providerId":"unknown","profileId":"default","model":"x","input":[{"role":"user","content":"oi"}]}`, "ERR_LLM_PROVIDER_UNKNOWN", http.StatusBadRequest)
}

func TestLLMLoginStatusNormalizesSessionErrors(t *testing.T) {
	handler := testHandler(t)
	if err := handler.addSession(loginSession{
		LoginSessionID: "expired-session",
		ProjectID:      "pockettrace",
		ProfileID:      "default",
		Mode:           "device_code",
		Status:         "expired",
		ExpiresAt:      1,
		Error:          security.NewError("ERR_LOGIN_SESSION_EXPIRED", "erro interno do fluxo", http.StatusGone),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/pockettrace/llm/login-sessions/expired-session?providerId=openai&profileId=default", nil)
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response LLMLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "expired" || response.Error == nil || response.Error.Code != "ERR_LLM_AUTH_EXPIRED" {
		t.Fatalf("response = %#v", response)
	}
	if stringsContainAny(rec.Body.String(), "ERR_LOGIN_SESSION_EXPIRED", "erro interno do fluxo") {
		t.Fatalf("resposta vazou erro interno: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/projects/pockettrace/llm/login-sessions/missing?providerId=openai&profileId=default", nil)
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var errorResponse MiddlewareErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errorResponse); err != nil {
		t.Fatal(err)
	}
	if errorResponse.Error.Code != "ERR_LLM_REQUEST_INVALID" {
		t.Fatalf("error = %#v", errorResponse.Error)
	}
}

func stringsContainAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if bytes.Contains([]byte(value), []byte(candidate)) {
			return true
		}
	}
	return false
}
