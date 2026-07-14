package lmstudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/security"
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

func TestValidateBaseURLRejectsAmbiguousParts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:1234?access_token=secret",
		"http://127.0.0.1:1234#fragment",
		"http://user:password@127.0.0.1:1234",
		"http://127.0.0.1:0",
	} {
		if err := ValidateBaseURL(raw); err == nil {
			t.Fatalf("ValidateBaseURL(%q) aceitou URL invalida", raw)
		}
	}
}

func TestTransportDoesNotFollowRedirectsWithAPIKey(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("API key chegou ao redirect target: %q", r.Header.Get("Authorization"))
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	_, err := NewTransport(source.Client()).ListModels(context.Background(), source.URL, "local-api-key")
	if err == nil {
		t.Fatal("esperava erro para redirect LM Studio")
	}
	if targetCalled {
		t.Fatal("transport seguiu redirect LM Studio")
	}
}

func TestTransportParsesMislabelledSSEAndKeepsMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("event: message\ndata: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
			"event: message\ndata: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\" lmstudio\"}}]}\n\n" +
			"event: message\ndata: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer server.Close()

	response, err := NewTransport(server.Client()).SendResponse(context.Background(), server.URL, "local-api-key", codex.CodexResponseRequest{
		Model: "model-a",
		Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}},
	})
	if err != nil {
		t.Fatalf("SendResponse() error = %v", err)
	}
	if response.OutputText != "ok lmstudio" || response.ResponseID != "chatcmpl-1" || response.Usage.TotalTokens != 5 {
		t.Fatalf("response = %#v", response)
	}
}

func TestTransportMapsProviderStatusWithoutLeakingBody(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		wantCode   string
		wantStatus int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: "ERR_LMSTUDIO_AUTH_REJECTED", wantStatus: http.StatusUnauthorized},
		{name: "bad-request", status: http.StatusBadRequest, wantCode: "ERR_LMSTUDIO_REQUEST_INVALID", wantStatus: http.StatusBadRequest},
		{name: "rate-limit", status: http.StatusTooManyRequests, wantCode: "ERR_LMSTUDIO_RATE_LIMITED", wantStatus: http.StatusTooManyRequests},
		{name: "server-error", status: http.StatusInternalServerError, wantCode: "ERR_LMSTUDIO_HTTP_FAILED", wantStatus: http.StatusBadGateway},
		{name: "gateway-timeout", status: http.StatusGatewayTimeout, wantCode: "ERR_LMSTUDIO_TIMEOUT", wantStatus: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			const canary = "provider-body-api-key-canary"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"api_key":"` + canary + `"}`))
			}))
			defer server.Close()

			_, err := NewTransport(server.Client()).ListModels(context.Background(), server.URL, "local-api-key")
			if security.Code(err) != test.wantCode || security.StatusCode(err) != test.wantStatus {
				t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("erro vazou corpo do provider: %v", err)
			}
		})
	}
}

func TestTransportMapsNetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 10 * time.Millisecond
	_, err := NewTransport(client).ListModels(context.Background(), server.URL, "local-api-key")
	if security.Code(err) != "ERR_LMSTUDIO_TIMEOUT" || security.StatusCode(err) != http.StatusGatewayTimeout {
		t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
	}
}
