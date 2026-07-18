package mcpstdio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mcpTestToken = "mcp-test-token-with-32-characters"

func TestServeInitializeAndToolsList(t *testing.T) {
	server := New(Options{MiddlewareToken: mcpTestToken})
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
`)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d output=%s", len(lines), output.String())
	}
	var initialize map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
		t.Fatal(err)
	}
	if initialize["jsonrpc"] != "2.0" {
		t.Fatalf("initialize response = %#v", initialize)
	}
	var tools map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &tools); err != nil {
		t.Fatal(err)
	}
	result := tools["result"].(map[string]any)
	if len(result["tools"].([]any)) == 0 {
		t.Fatalf("tools response = %#v", tools)
	}
}

func TestToolCallHealthUsesMiddleware(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","checks":[]}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"middleware_health","arguments":{}}}
`)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("invalid rpc response: %v output=%s", err, output.String())
	}
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("output = %s", output.String())
	}
	content := result["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), `"status"`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestServeRejectsUnsafeMCPConfiguration(t *testing.T) {
	for _, options := range []Options{
		{BaseURL: "https://example.com", MiddlewareToken: mcpTestToken},
		{BaseURL: "http://localhost:18787", MiddlewareToken: "short"},
	} {
		server := New(options)
		var output bytes.Buffer
		err := server.Serve(context.Background(), strings.NewReader(""), &output)
		if err == nil {
			t.Fatalf("Serve(%#v) aceitou configuracao invalida", options)
		}
	}
}

func TestToolCallOpenAILoginStatus(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/auth/openai/login-sessions/session-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+mcpTestToken {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"loginSessionId":"session-1","projectId":"acme","profileId":"default","mode":"device_code","status":"completed","expiresAt":123}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"openai_login_status","arguments":{"projectId":"acme","loginSessionId":"session-1"}}}
`)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("invalid rpc response: %v output=%s", err, output.String())
	}
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("output = %s", output.String())
	}
	content := result["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), `"completed"`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestToolCallCodexResponsesPassesModelControls(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/codex/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-5.5" {
			t.Fatalf("model = %#v", body["model"])
		}
		if body["intelligence"] != "thinking" {
			t.Fatalf("intelligence = %#v", body["intelligence"])
		}
		reasoning := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		if body["futureSelector"] != "next" {
			t.Fatalf("futureSelector = %#v", body["futureSelector"])
		}
		_, _ = w.Write([]byte(`{"events":[],"outputText":"ok"}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"codex_responses","arguments":{"projectId":"acme","input":"oi","model":"gpt-5.5","intelligence":"Thinking","reasoningEffort":"Estendido","extra":{"futureSelector":"next","model":"ignored"}}}}
`)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if !strings.Contains(output.String(), `"isError":false`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestLLMProvidersListsOpenAI(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/projects/acme/llm/providers" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("profileId") != "work" {
			t.Fatalf("profileId = %q", r.URL.Query().Get("profileId"))
		}
		if r.Header.Get("Authorization") != "Bearer "+mcpTestToken {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{
			"contractVersion":"middlewareauth.llm.v1",
			"providers":[
				{"id":"openai","defaults":{"profileId":"default","model":"gpt-5.5"}},
				{"id":"lmstudio","auth":{"defaultMode":"api_key"}}
			]
		}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_providers", map[string]any{"projectId": "acme", "profileId": "work"})
	if isErr {
		t.Fatalf("llm_providers error: %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	providers := response["providers"].([]any)
	openai := findProvider(t, providers, "openai")
	if openai["id"] != "openai" {
		t.Fatalf("provider = %#v", openai)
	}
	defaults := openai["defaults"].(map[string]any)
	if defaults["profileId"] != "default" || defaults["model"] != "gpt-5.5" {
		t.Fatalf("defaults = %#v", defaults)
	}
	lmstudio := findProvider(t, providers, "lmstudio")
	auth := lmstudio["auth"].(map[string]any)
	if auth["defaultMode"] != "api_key" {
		t.Fatalf("lmstudio auth = %#v", auth)
	}
}

func findProvider(t *testing.T, providers []any, id string) map[string]any {
	t.Helper()
	for _, provider := range providers {
		typed := provider.(map[string]any)
		if typed["id"] == id {
			return typed
		}
	}
	t.Fatalf("provider %s not found in %#v", id, providers)
	return nil
}

func TestLLMStatusMapsOpenAIResponse(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/llm/status" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("providerId") != "openai" || r.URL.Query().Get("profileId") != "default" {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"authenticated":true,"providerId":"openai","projectId":"acme","profileId":"default","accountId":"acc-1","email":"user@example.com","planType":"plus","expires":1780000000000}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_status", map[string]any{
		"providerId": "openai",
		"projectId":  "acme",
		"profileId":  "default",
	})
	if isErr {
		t.Fatalf("llm_status error: %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if response["providerId"] != "openai" || response["planType"] != "plus" || response["authenticated"] != true {
		t.Fatalf("response = %#v", response)
	}
}

func TestLLMLoginStartUsesGenericDeviceCodeContract(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/llm/login" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["profileId"] != "default" || body["mode"] != "device_code" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"providerId":"openai","projectId":"acme","profileId":"default","loginSessionId":"session-1","mode":"device_code","status":"pending","authenticated":false,"verificationUrl":"https://auth.example/device","userCode":"ABCD","expiresAt":1780000000000}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_login_start", map[string]any{
		"providerId": "openai",
		"projectId":  "acme",
		"profileId":  "default",
	})
	if isErr {
		t.Fatalf("llm_login_start error: %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if response["expiresAt"] != float64(1780000000000) || response["providerId"] != "openai" {
		t.Fatalf("response = %#v", response)
	}
}

func TestLLMLoginStatusMapsCompletedToAuthenticated(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/llm/login-sessions/session-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("providerId") != "openai" || r.URL.Query().Get("profileId") != "default" {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"providerId":"openai","loginSessionId":"session-1","projectId":"acme","profileId":"default","mode":"device_code","status":"authenticated","authenticated":true,"expiresAt":123000,"completedAt":122000}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_login_status", map[string]any{
		"providerId":     "openai",
		"projectId":      "acme",
		"loginSessionId": "session-1",
	})
	if isErr {
		t.Fatalf("llm_login_status error: %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "authenticated" || response["authenticated"] != true || response["profileId"] != "default" {
		t.Fatalf("response = %#v", response)
	}
}

func TestLLMResponsesUsesGenericHTTPContract(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/llm/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["providerId"] != "openai" || body["profileId"] != "default" || body["model"] != "gpt-5.5" || body["intelligence"] != "thinking" {
			t.Fatalf("body = %#v", body)
		}
		reasoning := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		if body["futureSelector"] != "next" {
			t.Fatalf("futureSelector = %#v", body["futureSelector"])
		}
		if body["service_tier"] != "priority" {
			t.Fatalf("service_tier = %#v", body["service_tier"])
		}
		outputContract := body["outputContract"].(map[string]any)
		if outputContract["id"] != "pockettrace.contract.v1" || outputContract["strict"] != true {
			t.Fatalf("outputContract = %#v", outputContract)
		}
		_, _ = w.Write([]byte(`{"events":[{"type":"response.output_text.delta","payload":"{\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}"}],"outputText":"ok pocketwiki"}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_responses", map[string]any{
		"providerId":      "openai",
		"projectId":       "acme",
		"profileId":       "default",
		"model":           "gpt-5.5",
		"input":           "oi",
		"intelligence":    "Thinking",
		"reasoningEffort": "Estendido",
		"serviceTier":     "priority",
		"outputContract": map[string]any{
			"id":         "pockettrace.contract.v1",
			"schemaHash": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"strict":     true,
			"jsonSchema": map[string]any{"type": "object", "additionalProperties": false},
		},
		"extra": map[string]any{"futureSelector": "next", "model": "ignored"},
	})
	if isErr {
		t.Fatalf("llm_responses error: %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if response["outputText"] != "ok pocketwiki" {
		t.Fatalf("response = %#v", response)
	}
}

func TestLLMStudioLoginStatusAndResponses(t *testing.T) {
	apiKey := "local-api-key"
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mcpTestToken {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/projects/acme/llm/login":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			authFields, ok := body["authFields"].(map[string]any)
			if !ok || body["providerId"] != "lmstudio" || body["profileId"] != "default" || body["mode"] != "api_key" || authFields["apiKey"] != apiKey || authFields["baseUrl"] != "http://127.0.0.1:1234" {
				t.Fatalf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"authenticated":true,"providerId":"lmstudio","projectId":"acme","profileId":"default","loginSessionId":"lmstudio-api-key-default","mode":"api_key","status":"authenticated","baseUrl":"http://127.0.0.1:1234","accountId":"lmstudio:127.0.0.1:1234","modelCount":1}`))
		case "/v1/projects/acme/llm/status":
			if r.URL.Query().Get("providerId") != "lmstudio" || r.URL.Query().Get("profileId") != "default" {
				t.Fatalf("query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"authenticated":true,"providerId":"lmstudio","projectId":"acme","profileId":"default","baseUrl":"http://127.0.0.1:1234","accountId":"lmstudio:127.0.0.1:1234"}`))
		case "/v1/projects/acme/llm/responses":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["providerId"] != "lmstudio" || body["profileId"] != "default" || body["model"] != "model-a" {
				t.Fatalf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"events":[{"type":"response.output_text.delta","payload":"{\"type\":\"response.output_text.delta\",\"delta\":\"ok lmstudio\"}"},{"type":"done"}],"outputText":"ok lmstudio"}`))
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client(), LMStudioModel: "model-a"})
	text, isErr := server.callTool(context.Background(), "llm_login_start", map[string]any{
		"providerId": "lmstudio",
		"projectId":  "acme",
		"profileId":  "default",
		"baseUrl":    "http://127.0.0.1:1234",
		"apiKey":     apiKey,
	})
	if isErr || strings.Contains(text, apiKey) {
		t.Fatalf("login text=%s isErr=%v", text, isErr)
	}
	text, isErr = server.callTool(context.Background(), "llm_status", map[string]any{
		"providerId": "lmstudio",
		"projectId":  "acme",
		"profileId":  "default",
	})
	if isErr || !strings.Contains(text, `"baseUrl"`) {
		t.Fatalf("status text=%s isErr=%v", text, isErr)
	}
	text, isErr = server.callTool(context.Background(), "llm_responses", map[string]any{
		"providerId": "lmstudio",
		"projectId":  "acme",
		"profileId":  "default",
		"model":      "model-a",
		"input":      "oi",
	})
	if isErr || !strings.Contains(text, "ok lmstudio") {
		t.Fatalf("responses text=%s isErr=%v", text, isErr)
	}
}

func TestLLMUnknownProviderReturnsStructuredError(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/acme/llm/status" || r.URL.Query().Get("providerId") != "anthropic" {
			t.Fatalf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"ERR_LLM_PROVIDER_UNKNOWN","message":"provider LLM desconhecido"}}`))
	}))
	defer httpServer.Close()

	server := New(Options{BaseURL: httpServer.URL, MiddlewareToken: mcpTestToken, HTTPClient: httpServer.Client()})
	text, isErr := server.callTool(context.Background(), "llm_status", map[string]any{
		"providerId": "anthropic",
		"projectId":  "acme",
		"profileId":  "default",
	})
	if !isErr {
		t.Fatalf("expected error, got %s", text)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	errBody := response["error"].(map[string]any)
	if errBody["code"] != "ERR_LLM_PROVIDER_UNKNOWN" || errBody["providerId"] != "anthropic" {
		t.Fatalf("error = %#v", errBody)
	}
}
