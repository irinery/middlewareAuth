package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestBuildCodexHeadersProtectsAuthorization(t *testing.T) {
	headers := BuildCodexHeaders("internal-access", "account-1", "origin", []HeaderPair{
		{Key: "Authorization", Value: "Bearer caller"},
		{Key: "X-Test", Value: "ok"},
	})
	if got := headers.Get("Authorization"); got != "Bearer internal-access" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("chatgpt-account-id"); got != "account-1" {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
	if got := headers.Get("X-Test"); got != "ok" {
		t.Fatalf("X-Test = %q", got)
	}
}

func TestResolveCodexResponsesURLAvoidsDuplicateCodex(t *testing.T) {
	got := ResolveCodexResponsesURL("https://chatgpt.com/backend-api/codex", "/codex/responses")
	want := "https://chatgpt.com/backend-api/codex/responses"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestListCodexModelsUsesAuthenticatedCatalogEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/codex/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("client_version") != "0.145.0" {
			t.Fatalf("client_version = %q", r.URL.Query().Get("client_version"))
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("chatgpt-account-id") != "account-1" {
			t.Fatalf("headers = %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.6-luna","display_name":"GPT-5.6-Luna","description":"Fast","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low","description":"Fast responses"},{"effort":"max","description":"Maximum reasoning"}],"visibility":"list","supported_in_api":true,"priority":3,"service_tiers":[{"id":"priority","name":"Fast","description":"1.5x speed"}]}]}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{
		BaseURL:          server.URL,
		ModelsPath:       "/codex/models",
		ClientVersion:    "0.145.0",
		RequestTimeoutMs: 5000,
	}, server.Client())
	models, err := transport.ListCodexModels(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"})
	if err != nil {
		t.Fatalf("ListCodexModels() error = %v", err)
	}
	if len(models) != 1 || models[0].Slug != "gpt-5.6-luna" || len(models[0].SupportedReasoningLevels) != 2 {
		t.Fatalf("models = %#v", models)
	}
	if len(models[0].ServiceTiers) != 1 || models[0].ServiceTiers[0].ID != "priority" {
		t.Fatalf("service tiers = %#v", models[0].ServiceTiers)
	}
}

func TestResolveCodexModelsURLAvoidsDuplicateCodex(t *testing.T) {
	got := ResolveCodexModelsURL("https://chatgpt.com/backend-api/codex", "/codex/models", "0.145.0")
	want := "https://chatgpt.com/backend-api/codex/models?client_version=0.145.0"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestTransportRetriesRateLimitAndParsesSSE(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		if calls == 1 {
			w.Header().Set("retry-after-ms", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{
		BaseURL:          server.URL,
		ResponsesPath:    "/codex/responses",
		RequestTimeoutMs: 5000,
		MaxRetries:       1,
	}, server.Client())
	response, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{
		Access:    "access",
		AccountID: "account-1",
	}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: 1})
	if err != nil {
		t.Fatalf("SendCodexResponse() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(response.Events) != 1 || response.Events[0].Type != "response.output_text.delta" {
		t.Fatalf("events = %#v", response.Events)
	}
	if response.OutputText != "ok" {
		t.Fatalf("OutputText = %q", response.OutputText)
	}
	time.Sleep(time.Millisecond)
}

func TestTransportParsesSSEBodyWithJSONContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" pocketwiki\"}\n\n"))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{
		BaseURL:          server.URL,
		ResponsesPath:    "/codex/responses",
		RequestTimeoutMs: 5000,
		MaxRetries:       1,
	}, server.Client())
	response, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{
		Access:    "access",
		AccountID: "account-1",
	}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: 1})
	if err != nil {
		t.Fatalf("SendCodexResponse() error = %v", err)
	}
	if len(response.Events) != 2 {
		t.Fatalf("events = %#v", response.Events)
	}
	if response.Events[0].Payload == "" || response.Events[0].Payload[0:1] != "{" {
		t.Fatalf("payload still looks nested: %#v", response.Events[0])
	}
	if response.OutputText != "ok pocketwiki" {
		t.Fatalf("OutputText = %q", response.OutputText)
	}
}

func TestTransportForwardsIntelligenceReasoningAndExtra(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["intelligence"] != "thinking" {
			t.Fatalf("intelligence = %#v body=%s", body["intelligence"], raw)
		}
		reasoning := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		if body["futureFlag"] != "enabled" {
			t.Fatalf("futureFlag = %#v body=%s", body["futureFlag"], raw)
		}
		_, _ = w.Write([]byte(`{"id":"resp","type":"response","outputText":"ok"}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{
		BaseURL:          server.URL,
		ResponsesPath:    "/codex/responses",
		RequestTimeoutMs: 5000,
		MaxRetries:       1,
	}, server.Client())
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{
		Access:    "access",
		AccountID: "account-1",
	}, CodexResponseRequest{
		Model:        "gpt-test",
		Intelligence: "thinking",
		Input:        []CodexInputItem{{Role: "user", Content: "oi"}},
		Reasoning:    &CodexReasoningConfig{Effort: "high"},
		Extra:        map[string]any{"futureFlag": "enabled", "model": "ignored"},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: 1})
	if err != nil {
		t.Fatalf("SendCodexResponse() error = %v", err)
	}
}

func TestTransportMapsProviderStatusWithoutLeakingBody(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		wantCode   string
		wantStatus int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: "ERR_CODEX_AUTH_REJECTED", wantStatus: http.StatusUnauthorized},
		{name: "bad-request", status: http.StatusBadRequest, wantCode: "ERR_CODEX_REQUEST_INVALID", wantStatus: http.StatusBadRequest},
		{name: "rate-limit", status: http.StatusTooManyRequests, wantCode: "ERR_CODEX_RATE_LIMITED", wantStatus: http.StatusTooManyRequests},
		{name: "server-error", status: http.StatusInternalServerError, wantCode: "ERR_CODEX_HTTP_FAILED", wantStatus: http.StatusBadGateway},
		{name: "gateway-timeout", status: http.StatusGatewayTimeout, wantCode: "ERR_CODEX_TIMEOUT", wantStatus: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			const canary = "provider-body-refresh-token-canary"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"refresh_token":"` + canary + `"}`))
			}))
			defer server.Close()

			transport := NewTransport(config.CodexConfig{
				BaseURL:          server.URL,
				ResponsesPath:    "/codex/responses",
				RequestTimeoutMs: 5000,
				MaxRetries:       0,
			}, server.Client())
			_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{
				Access:    "access",
				AccountID: "account-1",
			}, CodexResponseRequest{
				Model: "gpt-test",
				Input: []CodexInputItem{{Role: "user", Content: "oi"}},
			}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
			if security.Code(err) != test.wantCode || security.StatusCode(err) != test.wantStatus {
				t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("erro vazou corpo do provider: %v", err)
			}
		})
	}
}
