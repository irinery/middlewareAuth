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

func TestDecisionProvidersHaveSeparateHTTPContractAndEncryptedCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer test-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"models":[{"name":"jev-latest"}]}`))
		case "/api/v1/key":
			w.Write([]byte(`{"data":{"label":"test"}}`))
		case "/v1/systemone", "/api/alpha/decisions":
			var request struct {
				Model     string         `json:"model"`
				State     map[string]any `json:"state"`
				Questions map[string]any `json:"questions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode decision request: %v", err)
			}
			if request.State["ticket"] == nil || len(request.Questions) == 0 {
				t.Errorf("state/questions = %#v / %#v", request.State, request.Questions)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "decision_1", "model": request.Model,
				"answers": map[string]any{"refund": map[string]any{"type": "noul", "noul": 0.98}},
				"usage":   map[string]any{"input_tokens": 120, "output_tokens": 12},
			})
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
			http.NotFound(w, r)
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

	chatCatalog := call(http.MethodGet, "/v1/projects/acme/llm/providers", "")
	if chatCatalog.Code != http.StatusOK || strings.Contains(chatCatalog.Body.String(), "typesafe-jev") || strings.Contains(chatCatalog.Body.String(), "openrouter-jev") {
		t.Fatalf("Jev apareceu no catálogo de chat: %d %s", chatCatalog.Code, chatCatalog.Body.String())
	}
	decisionCatalog := call(http.MethodGet, "/v1/projects/acme/decisions/providers", "")
	if decisionCatalog.Code != http.StatusOK || !strings.Contains(decisionCatalog.Body.String(), "typesafe-jev") || !strings.Contains(decisionCatalog.Body.String(), "openrouter-jev") {
		t.Fatalf("catálogo de decisão: %d %s", decisionCatalog.Code, decisionCatalog.Body.String())
	}

	for _, test := range []struct {
		provider string
		key      string
	}{{"typesafe-jev", "test-typesafe-key"}, {"openrouter-jev", "test-openrouter-decision-key"}} {
		t.Run(test.provider, func(t *testing.T) {
			loginBody, _ := json.Marshal(map[string]any{"providerId": test.provider, "profileId": "default", "apiKey": test.key})
			login := call(http.MethodPost, "/v1/projects/acme/decisions/login", string(loginBody))
			if login.Code != http.StatusOK || strings.Contains(login.Body.String(), test.key) {
				t.Fatalf("login: %d %s", login.Code, login.Body.String())
			}
			stored, err := os.ReadFile(filepath.Join(h.cfg.StateDir, "auth-profiles.json"))
			if err != nil || strings.Contains(string(stored), test.key) {
				t.Fatalf("API key nao protegida no store: %v", err)
			}

			status := call(http.MethodGet, "/v1/projects/acme/decisions/status?providerId="+test.provider, "")
			if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"authenticated":true`) {
				t.Fatalf("status: %d %s", status.Code, status.Body.String())
			}

			requestBody := `{"providerId":"` + test.provider + `","profileId":"default","state":{"ticket":"Cobrado duas vezes; solicito estorno."},"questions":{"refund":{"type":"noul","instructions":"A pessoa pede reembolso?"}}}`
			response := call(http.MethodPost, "/v1/projects/acme/decisions/evaluate", requestBody)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"noul":0.98`) || !strings.Contains(response.Body.String(), `"input_tokens":120`) {
				t.Fatalf("evaluate: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
