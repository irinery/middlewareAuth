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
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestTransportTranslatesOutputContractToResponseFormat(t *testing.T) {
	publicSchema := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},"type":"object","properties":{"value":{"$ref":"#/$defs/value"}},"required":["value"],"additionalProperties":false}`)
	contract := &llmcontract.OutputContract{
		ID:         "pockettrace.contract.v1",
		SchemaHash: llmcontract.SchemaHash(publicSchema),
		Strict:     true,
		JSONSchema: publicSchema,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		format := body["response_format"].(map[string]any)
		jsonSchema := format["json_schema"].(map[string]any)
		if format["type"] != "json_schema" || jsonSchema["name"] != llmcontract.ProviderSchemaName(contract) || jsonSchema["strict"] != true {
			t.Fatalf("response_format=%#v", format)
		}
		schema := jsonSchema["schema"].(map[string]any)
		if _, exists := schema["$schema"]; exists || schema["$defs"] == nil {
			t.Fatalf("schema=%#v", schema)
		}
		if _, exists := body["outputContract"]; exists || strings.Contains(marshalForTest(body), "schemaHash") {
			t.Fatalf("portable metadata leaked to LM Studio wire: %#v", body)
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()

	_, err := NewTransport(server.Client()).SendResponse(context.Background(), server.URL, "local-api-key", codex.CodexResponseRequest{
		Model:          "local-model",
		Input:          []codex.CodexInputItem{{Role: "user", Content: "oi"}},
		OutputContract: contract,
		Extra:          map[string]any{"response_format": "must-not-override"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransportRejectsFencedStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"` + "```json\\n{}\\n```" + `"}}]}`))
	}))
	defer server.Close()

	schema := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	_, err := NewTransport(server.Client()).SendResponse(context.Background(), server.URL, "local-api-key", codex.CodexResponseRequest{
		Model: "local-model",
		Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}},
		OutputContract: &llmcontract.OutputContract{
			ID:         "schema.v1",
			SchemaHash: llmcontract.SchemaHash(schema),
			Strict:     true,
			JSONSchema: schema,
		},
	})
	if security.Code(err) != "ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED" {
		t.Fatalf("error=%v code=%s", err, security.Code(err))
	}
}

func TestTransportMapsOutputContractModelRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"model does not support schema"}`))
	}))
	defer server.Close()

	schema := json.RawMessage(`{"type":"object"}`)
	_, err := NewTransport(server.Client()).SendResponse(context.Background(), server.URL, "local-api-key", codex.CodexResponseRequest{
		Model: "small-model",
		Input: []codex.CodexInputItem{{Role: "user", Content: "oi"}},
		OutputContract: &llmcontract.OutputContract{
			ID:         "schema.v1",
			SchemaHash: llmcontract.SchemaHash(schema),
			Strict:     true,
			JSONSchema: schema,
		},
	})
	if security.Code(err) != "ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED" || security.StatusCode(err) != http.StatusUnprocessableEntity {
		t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
	}
}

func marshalForTest(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

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
