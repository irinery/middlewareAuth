package lmstudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/irinery/middlewareAuth/internal/codex"
)

func TestTransportListModelsAndSendResponse(t *testing.T) {
	apiKey := "local-api-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["model"] != "model-a" {
				t.Fatalf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"ok lmstudio"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	transport := NewTransport(server.Client())
	models, err := transport.ListModels(context.Background(), server.URL, apiKey)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("models = %#v", models)
	}
	response, err := transport.SendResponse(context.Background(), server.URL, apiKey, codex.CodexResponseRequest{
		Model:        "model-a",
		Instructions: "responda curto",
		Input:        []codex.CodexInputItem{{Role: "user", Content: "oi"}},
	})
	if err != nil {
		t.Fatalf("SendResponse() error = %v", err)
	}
	if response.OutputText != "ok lmstudio" {
		t.Fatalf("OutputText = %q", response.OutputText)
	}
	if response.Usage.TotalTokens != 5 {
		t.Fatalf("Usage = %#v", response.Usage)
	}
}

func TestValidateBaseURLRejectsPublicHost(t *testing.T) {
	if err := ValidateBaseURL("https://example.com"); err == nil {
		t.Fatalf("expected error")
	}
}
