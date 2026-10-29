package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestClaudeAPIKeyAndMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-claude-key" || r.Header.Get("anthropic-version") == "" || r.Header.Get("anthropic-workspace-id") != "wrkspc_test" {
			t.Errorf("headers incorretos: %v", r.Header)
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[]}`))
		case "/v1/messages":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "claude-test" || body["system"] != "responda curto" || body["max_tokens"] != float64(42) {
				t.Errorf("body incorreto: %#v", body)
			}
			w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":1}}`))
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	transport := NewTransport(server.Client())
	transport.AnthropicURL = server.URL
	if err := transport.VerifyKey(context.Background(), Anthropic, "test-claude-key", "wrkspc_test"); err != nil {
		t.Fatal(err)
	}
	response, err := transport.Send(context.Background(), Anthropic, "test-claude-key", "wrkspc_test", codex.CodexResponseRequest{Model: "claude-test", Instructions: "responda curto", Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}}, Extra: map[string]any{"max_output_tokens": float64(42)}})
	if err != nil {
		t.Fatal(err)
	}
	if response.OutputText != "ok" || response.Usage.TotalTokens != 4 {
		t.Fatalf("resposta incorreta: %#v", response)
	}
}

func TestOpenRouterKeyAndStreamingChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-router-key" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/key":
			w.Write([]byte(`{"data":{"label":"test"}}`))
		case "/api/v1/chat/completions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "anthropic/claude-test" || body["stream"] != true {
				t.Errorf("body incorreto: %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"id\":\"chat_1\",\"choices\":[{\"delta\":{\"content\":\"o\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"k\"}}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	transport := NewTransport(server.Client())
	transport.OpenRouterURL = server.URL
	if err := transport.VerifyKey(context.Background(), OpenRouter, "test-router-key", ""); err != nil {
		t.Fatal(err)
	}
	response, err := transport.Send(context.Background(), OpenRouter, "test-router-key", "", codex.CodexResponseRequest{Model: "anthropic/claude-test", Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if response.OutputText != "ok" || response.ResponseID != "chat_1" || response.Usage.TotalTokens != 3 {
		t.Fatalf("resposta incorreta: %#v", response)
	}
}

func TestKeyRejectionAndNoSecretInError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"test-secret"}`))
	}))
	defer server.Close()
	transport := NewTransport(server.Client())
	transport.OpenRouterURL = server.URL
	err := transport.VerifyKey(context.Background(), OpenRouter, "test-secret", "")
	if security.Code(err) != "ERR_CLOUD_AUTH_REJECTED" || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("erro inseguro: %v", err)
	}
}

func TestStreamProviderErrorDoesNotReturnPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\nevent: error\ndata: {\"error\":{\"message\":\"upstream failed\"}}\n\n"))
	}))
	defer server.Close()
	transport := NewTransport(server.Client())
	transport.OpenRouterURL = server.URL
	_, err := transport.Send(context.Background(), OpenRouter, "test-router-key", "", codex.CodexResponseRequest{Model: "test-model", Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}}, Stream: true})
	if security.Code(err) != "ERR_CLOUD_PROVIDER_FAILED" {
		t.Fatalf("erro esperado para stream interrompido: %v", err)
	}
}

func TestClaudeStreamUsageAndText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()
	transport := NewTransport(server.Client())
	transport.AnthropicURL = server.URL
	response, err := transport.Send(context.Background(), Anthropic, "test-claude-key", "", codex.CodexResponseRequest{Model: "claude-test", Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if response.OutputText != "ok" || response.ResponseID != "msg_2" || response.Usage.TotalTokens != 6 {
		t.Fatalf("stream incorreto: %#v", response)
	}
}

func TestStructuredOutputWireForCloudProviders(t *testing.T) {
	schema := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	contract := &llmcontract.OutputContract{ID: "test.Result.v1", SchemaHash: llmcontract.SchemaHash(schema), Strict: true, JSONSchema: schema}
	for _, provider := range []string{Anthropic, OpenRouter} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["outputContract"] != nil || body["schemaHash"] != nil {
					t.Errorf("metadata vazou: %#v", body)
				}
				var format map[string]any
				if provider == Anthropic {
					config, ok := body["output_config"].(map[string]any)
					if !ok {
						t.Fatalf("output_config ausente: %#v", body)
					}
					format, _ = config["format"].(map[string]any)
					w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":" {\"ok\":true} "}]}`))
				} else {
					responseFormat, ok := body["response_format"].(map[string]any)
					if !ok {
						t.Fatalf("response_format ausente: %#v", body)
					}
					format, _ = responseFormat["json_schema"].(map[string]any)
					w.Write([]byte(`{"id":"chat_1","choices":[{"message":{"content":" {\"ok\":true} "}}]}`))
				}
				if format == nil {
					t.Fatal("formato ausente")
				}
				wireSchema, _ := format["schema"].(map[string]any)
				if wireSchema["$schema"] != nil || wireSchema["type"] != "object" {
					t.Errorf("schema incorreto: %#v", wireSchema)
				}
			}))
			defer server.Close()
			transport := NewTransport(server.Client())
			transport.AnthropicURL, transport.OpenRouterURL = server.URL, server.URL
			response, err := transport.Send(context.Background(), provider, "test-key", "", codex.CodexResponseRequest{Model: "test-model", Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}}, OutputContract: contract})
			if err != nil {
				t.Fatal(err)
			}
			if response.OutputText != `{"ok":true}` {
				t.Errorf("outputText = %q", response.OutputText)
			}
		})
	}
}
