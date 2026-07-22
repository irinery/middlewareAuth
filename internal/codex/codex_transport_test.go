package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
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

func TestMarshalCodexWireRequestTranslatesOutputContract(t *testing.T) {
	publicSchema := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{"value":{"anyOf":[{"type":"string","minLength":1,"maxLength":120},{"type":"null"}]}},"type":"object","properties":{"kind":{"const":"validated"},"value":{"$ref":"#/$defs/value"},"items":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":8}},"required":["kind","value","items"],"additionalProperties":false}`)
	before := append([]byte(nil), publicSchema...)
	request := CodexResponseRequest{
		Model: "gpt-5.6-sol",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
		OutputContract: &llmcontract.OutputContract{
			ID:         "pockettrace.AIValidatedEnrichment.v1",
			SchemaHash: llmcontract.SchemaHash(publicSchema),
			Strict:     true,
			JSONSchema: publicSchema,
		},
		Extra: map[string]any{"text": "must-not-override", "max_output_tokens": float64(12000)},
	}
	raw, err := marshalCodexWireRequest(request, CodexTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text=%#v", body["text"])
	}
	format := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != llmcontract.ProviderSchemaName(request.OutputContract) || format["strict"] != true {
		t.Fatalf("format=%#v", format)
	}
	schema := format["schema"].(map[string]any)
	if _, exists := schema["$schema"]; exists || schema["$defs"] == nil || schema["properties"] == nil {
		t.Fatalf("schema=%#v", schema)
	}
	if string(request.OutputContract.JSONSchema) != string(before) || request.OutputContract.SchemaHash != llmcontract.SchemaHash(publicSchema) {
		t.Fatalf("public contract mutated: %#v", request.OutputContract)
	}
	if _, exists := body["outputContract"]; exists || bytes.Contains(raw, []byte("schemaHash")) {
		t.Fatalf("portable metadata leaked to Codex wire: %s", raw)
	}
	if _, exists := body["max_output_tokens"]; exists {
		t.Fatalf("unsupported portable budget leaked to Codex wire: %s", raw)
	}
}

func TestCodexOutputContractRejectionUsesStableErrorAndDoesNotLogSchema(t *testing.T) {
	const canary = "raw-schema-log-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"unsupported_response_format","message":"schema rejected: ` + canary + `"}}`))
	}))
	defer server.Close()

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 1000}, server.Client())
	schema := json.RawMessage(`{"type":"object","description":"` + canary + `"}`)
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account"}, CodexResponseRequest{
		Model: "gpt-5.6-sol",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
		OutputContract: &llmcontract.OutputContract{
			ID:         "schema.v1",
			SchemaHash: llmcontract.SchemaHash(schema),
			Strict:     true,
			JSONSchema: schema,
		},
	}, CodexTransportOptions{})
	if security.Code(err) != "ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED" || security.StatusCode(err) != http.StatusUnprocessableEntity {
		t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
	}
	if strings.Contains(logs.String(), canary) || strings.Contains(logs.String(), `"schema"`) {
		t.Fatalf("schema leaked to logs: %s", logs.String())
	}
}

func TestCodexOutputContractReturnsOnlyBareJSONObject(t *testing.T) {
	for _, test := range []struct {
		name       string
		outputText string
		wantError  bool
	}{
		{name: "object", outputText: `{"summary":"ok"}`},
		{name: "fenced", outputText: "```json\n{\"summary\":\"ok\"}\n```", wantError: true},
		{name: "prose", outputText: `Resultado: {"summary":"ok"}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				payload, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": test.outputText})
				_, _ = w.Write([]byte("event: response.output_text.delta\ndata: " + string(payload) + "\n\n"))
			}))
			defer server.Close()

			transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 1000}, server.Client())
			schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`)
			response, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account"}, CodexResponseRequest{
				Model: "gpt-5.6-sol",
				Input: []CodexInputItem{{Role: "user", Content: "oi"}},
				OutputContract: &llmcontract.OutputContract{
					ID:         "schema.v1",
					SchemaHash: llmcontract.SchemaHash(schema),
					Strict:     true,
					JSONSchema: schema,
				},
			}, CodexTransportOptions{})
			if test.wantError {
				if security.Code(err) != "ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED" || response != nil {
					t.Fatalf("response=%#v err=%v code=%s", response, err, security.Code(err))
				}
				return
			}
			if err != nil || response.OutputText != test.outputText {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}

func TestCollectOutputTextFallsBackToStructuredCompletionEvents(t *testing.T) {
	tests := []struct {
		name   string
		events []CodexStreamEvent
	}{
		{
			name: "output text done",
			events: []CodexStreamEvent{{
				Type:    "response.output_text.done",
				Payload: `{"type":"response.output_text.done","text":"{\"summary\":\"ok\"}"}`,
			}},
		},
		{
			name: "response completed",
			events: []CodexStreamEvent{{
				Type: "response.completed",
				Payload: `{"type":"response.completed","response":{"output":[{"type":"message","content":[` +
					`{"type":"output_text","text":"{\"summary\":\"ok\"}"}]}]}}`,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := collectOutputText(test.events); got != `{"summary":"ok"}` {
				t.Fatalf("collectOutputText() = %q", got)
			}
		})
	}
}

func TestReadUpstreamErrorSupportsTopLevelDetail(t *testing.T) {
	code, message := readUpstreamError(strings.NewReader(`{"detail":"Unsupported parameter: text.format"}`))
	if code != "" || message != "Unsupported parameter: text.format" {
		t.Fatalf("code=%q message=%q", code, message)
	}
}

func TestReadUpstreamErrorClassifiesNonJSONWithoutReturningBody(t *testing.T) {
	code, message := readUpstreamError(strings.NewReader(`invalid request containing text.format and secret-canary`))
	if code != "" || message != "text.format" {
		t.Fatalf("code=%q message=%q", code, message)
	}
}

func TestCodexUsageLimitReturnedAsHTTP400IsRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You've hit your usage limit. Purchase more credits."))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 1000}, server.Client())
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account"}, CodexResponseRequest{
		Model: "gpt-5.6-terra",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
	}, CodexTransportOptions{})
	if security.Code(err) != "ERR_CODEX_RATE_LIMITED" || security.StatusCode(err) != http.StatusTooManyRequests {
		t.Fatalf("err=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
	}
}

func TestResolveCodexResponsesURLAvoidsDuplicateCodex(t *testing.T) {
	got := ResolveCodexResponsesURL("https://chatgpt.com/backend-api/codex", "/codex/responses")
	want := "https://chatgpt.com/backend-api/codex/responses"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestListCodexModelsUsesAuthenticatedCatalogEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/codex/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("client_version") != "0.145.0" {
			t.Fatalf("client_version = %q", r.URL.Query().Get("client_version"))
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("chatgpt-account-id") != "account-1" {
			t.Fatalf("headers = %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.6-luna","display_name":"GPT-5.6-Luna","description":"Fast","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low","description":"Fast responses"},{"effort":"max","description":"Maximum reasoning"}],"visibility":"list","supported_in_api":true,"priority":3,"service_tiers":[{"id":"priority","name":"Fast","description":"1.5x speed"}]}]}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{
		BaseURL:          server.URL,
		ModelsPath:       "/codex/models",
		ClientVersion:    "0.145.0",
		RequestTimeoutMs: 5000,
	}, server.Client())
	models, err := transport.ListCodexModels(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"})
	if err != nil {
		t.Fatalf("ListCodexModels() error = %v", err)
	}
	if len(models) != 1 || models[0].Slug != "gpt-5.6-luna" || len(models[0].SupportedReasoningLevels) != 2 {
		t.Fatalf("models = %#v", models)
	}
	if len(models[0].ServiceTiers) != 1 || models[0].ServiceTiers[0].ID != "priority" {
		t.Fatalf("service tiers = %#v", models[0].ServiceTiers)
	}
}

func TestResolveCodexModelsURLAvoidsDuplicateCodex(t *testing.T) {
	got := ResolveCodexModelsURL("https://chatgpt.com/backend-api/codex", "/codex/models", "0.145.0")
	want := "https://chatgpt.com/backend-api/codex/models?client_version=0.145.0"
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

func TestTransportDoesNotMixReasoningSummaryIntoOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"pensamento interno\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"resposta final\"}\n\n"))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 5000}, server.Client())
	response, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	if response.OutputText != "resposta final" {
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
		if _, found := body["intelligence"]; found {
			t.Fatalf("campo interno intelligence vazou para o protocolo Codex: %s", raw)
		}
		reasoning := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		input := body["input"].([]any)
		message := input[0].(map[string]any)
		content := message["content"].([]any)[0].(map[string]any)
		if message["type"] != "message" || content["type"] != "input_text" || content["text"] != "oi" {
			t.Fatalf("input Codex incompatível: %s", raw)
		}
		if body["tool_choice"] != "auto" || body["stream"] != true || body["store"] != false {
			t.Fatalf("campos obrigatórios Codex ausentes: %s", raw)
		}
		include := body["include"].([]any)
		if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("include = %#v body=%s", include, raw)
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

func TestTransportSerializesAssistantOutputAsOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []struct {
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body.Input[0].Content[0].Type; got != "output_text" {
			t.Fatalf("assistant content type = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"resp","type":"response"}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 5000}, server.Client())
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "assistant", Content: "resposta anterior"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransportMapsSystemMessageToCodexDeveloperRole(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []struct {
				Role string `json:"role"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body.Input[0].Role; got != "developer" {
			t.Fatalf("system wire role = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"resp","type":"response"}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 5000}, server.Client())
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "system", Content: "instrução"}, {Role: "user", Content: "oi"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransportMapsProviderStatusWithoutLeakingBody(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		wantCode   string
		wantStatus int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: "ERR_CODEX_AUTH_REJECTED", wantStatus: http.StatusUnauthorized},
		{name: "bad-request", status: http.StatusBadRequest, wantCode: "ERR_CODEX_REQUEST_INVALID", wantStatus: http.StatusBadRequest},
		{name: "rate-limit", status: http.StatusTooManyRequests, wantCode: "ERR_CODEX_RATE_LIMITED", wantStatus: http.StatusTooManyRequests},
		{name: "server-error", status: http.StatusInternalServerError, wantCode: "ERR_CODEX_HTTP_FAILED", wantStatus: http.StatusBadGateway},
		{name: "gateway-timeout", status: http.StatusGatewayTimeout, wantCode: "ERR_CODEX_TIMEOUT", wantStatus: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			const canary = "provider-body-refresh-token-canary"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"refresh_token":"` + canary + `"}`))
			}))
			defer server.Close()

			transport := NewTransport(config.CodexConfig{
				BaseURL:          server.URL,
				ResponsesPath:    "/codex/responses",
				RequestTimeoutMs: 5000,
				MaxRetries:       0,
			}, server.Client())
			_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{
				Access:    "access",
				AccountID: "account-1",
			}, CodexResponseRequest{
				Model: "gpt-test",
				Input: []CodexInputItem{{Role: "user", Content: "oi"}},
			}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
			if security.Code(err) != test.wantCode || security.StatusCode(err) != test.wantStatus {
				t.Fatalf("error=%v code=%s status=%d", err, security.Code(err), security.StatusCode(err))
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("erro vazou corpo do provider: %v", err)
			}
		})
	}
}

func TestTransportReturnsOnlySanitizedProviderErrorDetail(t *testing.T) {
	tokenCanary := "sk-" + "secret-provider-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"campo input inválido; token ` + tokenCanary + `"},"raw_secret":"não pode aparecer"}`))
	}))
	defer server.Close()

	transport := NewTransport(config.CodexConfig{BaseURL: server.URL, ResponsesPath: "/codex/responses", RequestTimeoutMs: 5000}, server.Client())
	_, err := transport.SendCodexResponse(context.Background(), auth.StoredOAuthCredential{Access: "access", AccountID: "account-1"}, CodexResponseRequest{
		Model: "gpt-test",
		Input: []CodexInputItem{{Role: "user", Content: "oi"}},
	}, CodexTransportOptions{TimeoutMs: 5000, MaxRetries: -1})
	public := security.Public(err)
	if len(public.Details) != 1 || public.Details[0].Field != "provider" {
		t.Fatalf("details = %#v", public.Details)
	}
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), tokenCanary) || strings.Contains(string(encoded), "raw_secret") {
		t.Fatalf("detalhe vazou segredo/corpo bruto: %s", encoded)
	}
}
