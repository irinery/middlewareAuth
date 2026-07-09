package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const clientTestToken = "client-test-token-with-32-characters"

func TestCodexResponsesUsesMiddlewareTokenAndNormalizesURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/codex/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("profileId") != "default" {
			t.Fatalf("profileId = %s", r.URL.Query().Get("profileId"))
		}
		if r.Header.Get("Authorization") != "Bearer "+clientTestToken {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["intelligence"] != "thinking" {
			t.Fatalf("intelligence = %#v body=%s", body["intelligence"], raw)
		}
		if body["futureFlag"] != "enabled" {
			t.Fatalf("futureFlag = %#v body=%s", body["futureFlag"], raw)
		}
		_ = json.NewEncoder(w).Encode(CodexResponseStream{Events: []CodexStreamEvent{{Type: "ok"}}, OutputText: "ok pocketwiki"})
	}))
	defer server.Close()

	c, err := NewClient(ClientOptions{BaseURL: server.URL + "/", MiddlewareToken: clientTestToken, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	response, err := c.Codex.Responses(context.Background(), "acme", "default", CodexResponseRequest{
		Model:        "gpt-test",
		Intelligence: "thinking",
		Input:        []CodexInputItem{{Role: "user", Content: "oi"}},
		Extra:        map[string]any{"futureFlag": "enabled"},
	})
	if err != nil {
		t.Fatalf("Responses() error = %v", err)
	}
	if len(response.Events) != 1 || response.Events[0].Type != "ok" {
		t.Fatalf("response = %#v", response)
	}
	if response.OutputText != "ok pocketwiki" {
		t.Fatalf("OutputText = %q", response.OutputText)
	}
}

func TestClientMapsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"ERR_MIDDLEWARE_UNAUTHORIZED","message":"unauthorized"}}`))
	}))
	defer server.Close()

	c, err := NewClient(ClientOptions{BaseURL: server.URL, MiddlewareToken: clientTestToken, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Codex.Responses(context.Background(), "acme", "default", CodexResponseRequest{})
	clientErr, ok := err.(*ClientError)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if clientErr.Code != "ERR_MIDDLEWARE_UNAUTHORIZED" {
		t.Fatalf("code = %s", clientErr.Code)
	}
}

func TestNewClientRequiresMiddlewareToken(t *testing.T) {
	_, err := NewClient(ClientOptions{BaseURL: "http://localhost:18787"})
	clientErr, ok := err.(*ClientError)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if clientErr.Code != "ERR_CLIENT_AUTH_TOKEN_MISSING" {
		t.Fatalf("code = %s", clientErr.Code)
	}
}

func TestNewClientRejectsUnsafeBaseURLAndShortToken(t *testing.T) {
	for _, options := range []ClientOptions{
		{BaseURL: "http://user:password@localhost:18787", MiddlewareToken: clientTestToken},
		{BaseURL: "http://localhost:18787?token=secret", MiddlewareToken: clientTestToken},
		{BaseURL: "http://localhost:18787", MiddlewareToken: "short"},
	} {
		_, err := NewClient(options)
		if err == nil {
			t.Fatalf("NewClient(%#v) aceitou configuracao invalida", options)
		}
	}
}

func TestClientDoesNotRetryPOST(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "indisponivel", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c, err := NewClient(ClientOptions{BaseURL: server.URL, MiddlewareToken: clientTestToken, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Codex.Responses(context.Background(), "acme", "default", CodexResponseRequest{Model: "gpt-test", Input: []CodexInputItem{{Role: "user", Content: "oi"}}})
	if err == nil {
		t.Fatal("esperava erro HTTP")
	}
	if calls != 1 {
		t.Fatalf("POST foi repetido %d vezes", calls)
	}
}

func TestClientDoesNotFollowRedirectsWithMiddlewareToken(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("token chegou ao redirect target: %q", r.Header.Get("Authorization"))
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	c, err := NewClient(ClientOptions{BaseURL: source.URL, MiddlewareToken: clientTestToken, HTTPClient: source.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Codex.Responses(context.Background(), "acme", "default", CodexResponseRequest{Model: "gpt-test", Input: []CodexInputItem{{Role: "user", Content: "oi"}}})
	if err == nil {
		t.Fatal("esperava erro para redirect")
	}
	if targetCalled {
		t.Fatal("client seguiu redirect com bearer interno")
	}
}
