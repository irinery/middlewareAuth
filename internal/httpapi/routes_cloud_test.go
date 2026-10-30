package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloudKeysThroughCanonicalHTTPContract(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer test-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[]}`))
		case "/api/v1/key":
			w.Write([]byte(`{"data":{"label":"test"}}`))
		case "/v1/messages":
			w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"claude ok"}],"usage":{"input_tokens":2,"output_tokens":2}}`))
		case "/api/v1/chat/completions":
			w.Write([]byte(`{"id":"chat_1","choices":[{"message":{"content":"router ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":2}}`))
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	h := testHandler(t)
	h.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Scheme = "http"
		copy.URL.Host = strings.TrimPrefix(upstream.URL, "http://")
		return upstream.Client().Do(copy)
	})}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+handlerTestToken)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, test := range []struct{ provider, key, output string }{{"anthropic", "test-claude", "claude ok"}, {"openrouter", "test-router", "router ok"}} {
		login, _ := json.Marshal(map[string]any{"providerId": test.provider, "mode": "api_key", "authFields": map[string]string{"apiKey": test.key}})
		rec := call(http.MethodPost, "/v1/projects/acme/llm/login", string(login))
		if rec.Code != 200 || strings.Contains(rec.Body.String(), test.key) {
			t.Fatalf("login %s: %d %s", test.provider, rec.Code, rec.Body.String())
		}
		stored, err := os.ReadFile(filepath.Join(h.cfg.StateDir, "auth-profiles.json"))
		if err != nil || strings.Contains(string(stored), test.key) {
			t.Fatalf("chave %s nao protegida no store: %v", test.provider, err)
		}
		rec = call(http.MethodGet, "/v1/projects/acme/llm/status?providerId="+test.provider, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"authenticated":true`) {
			t.Fatalf("status %s: %d %s", test.provider, rec.Code, rec.Body.String())
		}
		response, _ := json.Marshal(map[string]any{"providerId": test.provider, "model": "test-model", "input": []map[string]string{{"role": "user", "content": "oi"}}})
		rec = call(http.MethodPost, "/v1/projects/acme/llm/responses", string(response))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), test.output) {
			t.Fatalf("response %s: %d %s", test.provider, rec.Code, rec.Body.String())
		}
		rec = call(http.MethodGet, "/v1/projects/outro/llm/status?providerId="+test.provider, "")
		if rec.Code != 200 || strings.Contains(rec.Body.String(), `"authenticated":true`) {
			t.Fatalf("isolamento %s: %d %s", test.provider, rec.Code, rec.Body.String())
		}
	}
}

func TestClaudeSubscriptionTokenIsNotStoredAsAPIKey(t *testing.T) {
	h := testHandler(t)
	_, err := h.configureCloudKey(httptest.NewRequest(http.MethodGet, "/", nil).Context(), providerAnthropic, "acme", "default", strings.Join([]string{"sk", "ant", "oat01-example"}, "-"), "")
	if err == nil {
		t.Fatal("token de assinatura aceito")
	}
}

func TestCloudProvidersAcceptOnlyAPIKeyMode(t *testing.T) {
	h := testHandler(t)
	for _, providerID := range []string{providerAnthropic, providerOpenRouter} {
		for _, mode := range []string{"oauth", "device_code"} {
			body, _ := json.Marshal(map[string]string{"providerId": providerID, "mode": mode})
			req := httptest.NewRequest(http.MethodPost, "/v1/projects/acme/llm/login", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+handlerTestToken)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ERR_LLM_REQUEST_INVALID") {
				t.Fatalf("%s/%s: status=%d body=%s", providerID, mode, rec.Code, rec.Body.String())
			}
		}
	}
}
