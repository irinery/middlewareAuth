package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/config"
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
