package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyKeyUsesProviderModelAndKeyEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-decision-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.Write([]byte(`{"models":[{"name":"jev-latest"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/key":
			w.Write([]byte(`{"data":{"label":"test"}}`))
		default:
			t.Errorf("request inesperado: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transport := NewTransport(server.Client())
	transport.TypeSafeURL = server.URL
	transport.OpenRouterURL = server.URL
	for _, provider := range []string{TypeSafe, OpenRouter} {
		t.Run(provider, func(t *testing.T) {
			if err := transport.VerifyKey(context.Background(), provider, "test-decision-key"); err != nil {
				t.Fatalf("VerifyKey() error = %v", err)
			}
		})
	}
}

func TestEvaluateSendsBatchedTypedQuestionsAndPreservesAnswers(t *testing.T) {
	const answerFixture = `{"id":"decision_123","model":"jev-latest","answers":{"refund":{"type":"noul","noul":0.98},"route":{"type":"choice","choice":"billing","probabilities":{"billing":0.96,"support":0.04},"confidence":0.96},"severity":{"type":"score","score":2.4,"legend":["none","mild","serious"]}},"usage":{"input_tokens":120,"output_tokens":12}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-decision-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		switch {
		case r.URL.Path == "/v1/systemone":
		case r.URL.Path == "/api/alpha/decisions":
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if request.State == nil || len(request.Questions) != 3 {
			t.Errorf("request state/questions = %#v / %#v", request.State, request.Questions)
		}
		for id, wantType := range map[string]string{"refund": "noul", "route": "choice", "severity": "score"} {
			question, ok := request.Questions[id].(map[string]any)
			if !ok || question["type"] != wantType {
				t.Errorf("questions[%q] = %#v; want type %q", id, request.Questions[id], wantType)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(answerFixture))
	}))
	defer server.Close()

	transport := NewTransport(server.Client())
	transport.TypeSafeURL = server.URL
	transport.OpenRouterURL = server.URL
	for _, test := range []struct {
		provider string
		model    string
	}{{TypeSafe, "jev-latest"}, {OpenRouter, "~typesafe/jev-latest"}} {
		t.Run(test.provider, func(t *testing.T) {
			response, err := transport.Evaluate(context.Background(), test.provider, "test-decision-key", Request{
				Model: test.model,
				State: map[string]any{"ticket": "Cobrado duas vezes; solicito estorno."},
				Questions: map[string]any{
					"refund":   map[string]any{"type": "noul", "instructions": "A pessoa pede reembolso?"},
					"route":    map[string]any{"type": "choice", "instructions": "Qual equipe?", "criteria": map[string]string{"billing": "Cobrança", "support": "Suporte"}},
					"severity": map[string]any{"type": "score", "instructions": "Qual a gravidade?", "criteria": []string{"none", "mild", "serious"}},
				},
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if response.Model != "jev-latest" || response.ID != "decision_123" {
				t.Fatalf("response metadata = %#v", response)
			}
			var answers map[string]map[string]any
			if err := json.Unmarshal(response.Answers, &answers); err != nil {
				t.Fatal(err)
			}
			if answers["refund"]["type"] != "noul" || answers["refund"]["noul"] != 0.98 {
				t.Errorf("noul answer not preserved: %#v", answers["refund"])
			}
			if answers["route"]["type"] != "choice" || answers["route"]["choice"] != "billing" {
				t.Errorf("choice answer not preserved: %#v", answers["route"])
			}
			if answers["severity"]["type"] != "score" || answers["severity"]["score"] != 2.4 {
				t.Errorf("score answer not preserved: %#v", answers["severity"])
			}
			if !strings.Contains(string(response.Usage), `"input_tokens":120`) {
				t.Errorf("usage not preserved: %s", response.Usage)
			}
		})
	}
}

func TestEvaluateRejectsIncompleteRequestBeforeCallingProvider(t *testing.T) {
	transport := NewTransport(nil)
	_, err := transport.Evaluate(context.Background(), TypeSafe, "key", Request{Model: "jev-latest", State: "text"})
	if err == nil || !strings.Contains(err.Error(), "questions obrigatorios") {
		t.Fatalf("Evaluate() error = %v", err)
	}
}
