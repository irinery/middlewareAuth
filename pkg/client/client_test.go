package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexResponsesUsesMiddlewareTokenAndNormalizesURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/codex/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("profileId") != "default" {
			t.Fatalf("profileId = %s", r.URL.Query().Get("profileId"))
		}
		if r.Header.Get("Authorization") != "Bearer mw-token" {
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

	c, err := NewClient(ClientOptions{BaseURL: server.URL + "/", MiddlewareToken: "mw-token", HTTPClient: server.Client()})
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

	c, err := NewClient(ClientOptions{BaseURL: server.URL, MiddlewareToken: "mw-token", HTTPClient: server.Client()})
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
