package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
)

type Transport struct {
	cfg    config.CodexConfig
	client *http.Client
}

const maxResponseBytes = 5 << 20

func NewTransport(cfg config.CodexConfig, client *http.Client) *Transport {
	if client == nil {
		client = config.NewHTTPClient(cfg)
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Transport{cfg: cfg, client: &copy}
}

func SendCodexResponse(ctx context.Context, credential auth.StoredOAuthCredential, request CodexResponseRequest, options CodexTransportOptions) (*CodexResponseStream, error) {
	cfg := config.CodexConfig{
		BaseURL:          "https://chatgpt.com/backend-api",
		ResponsesPath:    "/codex/responses",
		ModelsPath:       "/codex/models",
		ClientVersion:    "0.145.0",
		RequestTimeoutMs: 30000,
		MaxRetries:       3,
	}
	return NewTransport(cfg, config.NewHTTPClient(cfg)).SendCodexResponse(ctx, credential, request, options)
}

func (t *Transport) ListCodexModels(ctx context.Context, credential auth.StoredOAuthCredential) ([]CodexModelInfo, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	if credential.Access == "" || credential.AccountID == "" {
		return nil, security.NewError("ERR_CODEX_REQUEST_INVALID", "credencial Codex incompleta", http.StatusBadRequest)
	}

	timeoutMs := t.cfg.RequestTimeoutMs
	if timeoutMs < 1000 {
		timeoutMs = 1000
	}
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ResolveCodexModelsURL(t.cfg.BaseURL, t.cfg.ModelsPath, t.cfg.ClientVersion), nil)
	if err != nil {
		return nil, security.Wrap("ERR_CODEX_HTTP_FAILED", "falha ao montar consulta de modelos Codex", http.StatusInternalServerError, err)
	}
	headers := BuildCodexHeaders(credential.Access, credential.AccountID, "middleware-codex-oauth", nil)
	headers.Set("Accept", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return nil, mapContextErr(reqCtx.Err())
		}
		return nil, security.Wrap("ERR_CODEX_HTTP_FAILED", "falha HTTP ao consultar modelos Codex", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, security.NewError("ERR_CODEX_AUTH_REJECTED", "Codex recusou autenticacao", http.StatusUnauthorized)
		}
		return nil, security.NewError("ERR_CODEX_HTTP_FAILED", "Codex recusou consulta de modelos", http.StatusBadGateway)
	}
	raw, err := readLimitedResponse(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	var result CodexModelsResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, security.Wrap("ERR_CODEX_RESPONSE_INVALID", "catalogo de modelos Codex invalido", http.StatusBadGateway, err)
	}
	return result.Models, nil
}

func (t *Transport) SendCodexResponse(ctx context.Context, credential auth.StoredOAuthCredential, request CodexResponseRequest, options CodexTransportOptions) (*CodexResponseStream, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	if credential.Access == "" {
		return nil, security.NewError("ERR_CODEX_REQUEST_INVALID", "access token ausente", http.StatusBadRequest)
	}
	if credential.AccountID == "" {
		return nil, security.NewError("ERR_CODEX_ACCOUNT_ID_MISSING", "accountId ausente", http.StatusBadRequest)
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	options = t.defaults(options)
	raw, err := marshalCodexWireRequest(request, options)
	if err != nil {
		return nil, security.Wrap("ERR_CODEX_REQUEST_INVALID", "payload Codex invalido", http.StatusBadRequest, err)
	}
	if len(raw) > 2<<20 {
		return nil, security.NewError("ERR_CODEX_REQUEST_INVALID", "payload Codex excede 2 MB", http.StatusBadRequest)
	}

	var lastErr error
	for attempt := 0; attempt <= options.MaxRetries; attempt++ {
		response, retry, delay, err := t.sendOnce(ctx, credential, raw, options, request.OutputContract != nil)
		if err == nil {
			if request.OutputContract != nil {
				response.OutputText, err = llmcontract.NormalizeStructuredOutputText(response.OutputText)
				if err != nil {
					return nil, err
				}
			}
			return response, nil
		}
		lastErr = err
		if !retry || attempt == options.MaxRetries {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, mapContextErr(ctx.Err())
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (t *Transport) sendOnce(ctx context.Context, credential auth.StoredOAuthCredential, raw []byte, options CodexTransportOptions, hasOutputContract bool) (*CodexResponseStream, bool, time.Duration, error) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(options.TimeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ResolveCodexResponsesURL(t.cfg.BaseURL, t.cfg.ResponsesPath), bytes.NewReader(raw))
	if err != nil {
		return nil, false, 0, security.Wrap("ERR_CODEX_HTTP_FAILED", "falha ao montar request Codex", http.StatusInternalServerError, err)
	}
	headers := BuildCodexHeaders(credential.Access, credential.AccountID, "middleware-codex-oauth", options.AdditionalHeaders)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if options.SessionID != "" {
		req.Header.Set("codex-session-id", options.SessionID)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return nil, false, 0, mapContextErr(reqCtx.Err())
		}
		return nil, true, time.Second, security.Wrap("ERR_CODEX_HTTP_FAILED", "falha HTTP ao chamar Codex", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		upstreamCode, upstreamMessage := readUpstreamError(resp.Body)
		if hasOutputContract {
			slog.WarnContext(ctx, "codex_request_rejected",
				slog.Int("status", resp.StatusCode),
				slog.Bool("output_contract", true),
			)
		} else if upstreamCode != "" || upstreamMessage != "" {
			slog.WarnContext(ctx, "codex_request_rejected",
				slog.Int("status", resp.StatusCode),
				slog.String("upstream_code", upstreamCode),
				slog.String("upstream_message", upstreamMessage),
			)
		}
		delay := retryDelay(resp, 1000)
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, false, 0, security.NewError("ERR_CODEX_AUTH_REJECTED", "Codex recusou autenticacao", http.StatusUnauthorized)
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return nil, true, delay, security.NewError("ERR_CODEX_TIMEOUT", "timeout ao chamar Codex", http.StatusGatewayTimeout)
		case http.StatusTooManyRequests:
			return nil, true, delay, security.NewError("ERR_CODEX_RATE_LIMITED", "Codex aplicou rate limit", http.StatusTooManyRequests)
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return nil, true, delay, security.NewError("ERR_CODEX_HTTP_FAILED", "Codex indisponivel", http.StatusBadGateway)
		default:
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				if isUsageLimitRejection(upstreamCode, upstreamMessage) {
					return nil, false, 0, security.NewError("ERR_CODEX_RATE_LIMITED", "Codex atingiu limite de uso", http.StatusTooManyRequests)
				}
				if hasOutputContract {
					return nil, false, 0, llmcontract.UnsupportedOutputContractWithReason(classifyOutputContractRejection(upstreamCode, upstreamMessage))
				}
				rejected := security.NewError("ERR_CODEX_REQUEST_INVALID", "Codex recusou o request", http.StatusBadRequest)
				if detail := upstreamErrorDetail(upstreamCode, upstreamMessage); detail != "" {
					rejected = security.WithDetail(rejected, "provider", detail)
				}
				return nil, false, 0, rejected
			}
			return nil, false, 0, security.NewError("ERR_CODEX_HTTP_FAILED", "Codex retornou status inesperado", http.StatusBadGateway)
		}
	}
	return parseResponse(resp)
}

func classifyOutputContractRejection(code, message string) string {
	value := strings.ToLower(code + " " + message)
	for _, candidate := range []struct {
		needle string
		reason string
	}{
		{"text.format", "text_format"},
		{"response_format", "response_format"},
		{"json_schema", "json_schema"},
		{"schema", "schema"},
		{"max_output_tokens", "max_output_tokens"},
		{"tools", "tools"},
		{"include", "include"},
	} {
		if strings.Contains(value, candidate.needle) {
			return candidate.reason
		}
	}
	if strings.TrimSpace(code) != "" {
		return "provider_code"
	}
	return "request_rejected"
}

type codexWireContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexWireInputItem struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []codexWireContent `json:"content"`
}

type codexWireRequest struct {
	Model             string                `json:"model"`
	Instructions      string                `json:"instructions,omitempty"`
	Input             []codexWireInputItem  `json:"input"`
	Tools             []CodexToolDefinition `json:"tools"`
	ToolChoice        string                `json:"tool_choice"`
	ParallelToolCalls bool                  `json:"parallel_tool_calls"`
	Reasoning         *CodexReasoningConfig `json:"reasoning,omitempty"`
	Store             bool                  `json:"store"`
	Stream            bool                  `json:"stream"`
	Include           []string              `json:"include"`
	PromptCacheKey    string                `json:"prompt_cache_key,omitempty"`
	Text              *codexWireTextConfig  `json:"text,omitempty"`
}

type codexWireTextConfig struct {
	Format codexWireJSONSchemaFormat `json:"format"`
}

type codexWireJSONSchemaFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

// marshalCodexWireRequest adapts the stable MiddlewareAuth contract to the
// current Codex Responses wire protocol. The public contract intentionally
// keeps content as a string so LM Studio and existing Pocket clients remain
// independent from provider-specific request shapes.
func marshalCodexWireRequest(request CodexResponseRequest, options CodexTransportOptions) ([]byte, error) {
	input := make([]codexWireInputItem, 0, len(request.Input))
	for _, item := range request.Input {
		role := item.Role
		if role == "system" {
			role = "developer"
		}
		contentType := "input_text"
		if role == "assistant" {
			contentType = "output_text"
		}
		input = append(input, codexWireInputItem{
			Type: "message",
			Role: role,
			Content: []codexWireContent{{
				Type: contentType,
				Text: item.Content,
			}},
		})
	}

	wire := codexWireRequest{
		Model:             request.Model,
		Instructions:      request.Instructions,
		Input:             input,
		Tools:             request.Tools,
		ToolChoice:        "auto",
		ParallelToolCalls: false,
		Reasoning:         request.Reasoning,
		Store:             false,
		Stream:            true,
		Include:           []string{"reasoning.encrypted_content"},
		PromptCacheKey:    options.SessionID,
	}
	if contract := request.OutputContract; contract != nil {
		providerSchema, err := llmcontract.ProviderJSONSchema(contract)
		if err != nil {
			return nil, err
		}
		wire.Text = &codexWireTextConfig{Format: codexWireJSONSchemaFormat{
			Type:   "json_schema",
			Name:   llmcontract.ProviderSchemaName(contract),
			Strict: contract.Strict,
			Schema: providerSchema,
		}}
	}
	raw, err := json.Marshal(wire)
	if err != nil || len(request.Extra) == 0 {
		return raw, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	for key, value := range request.Extra {
		if _, protected := payload[key]; protected || codexUnsupportedPortableField(key) {
			continue
		}
		payload[key] = value
	}
	return json.Marshal(payload)
}

func codexUnsupportedPortableField(key string) bool {
	switch key {
	case "max_output_tokens":
		// The public MiddlewareAuth contract accepts this portable budget, but
		// ChatGPT's Codex responses endpoint does not expose that field.
		return true
	default:
		return false
	}
}

func readUpstreamError(body io.Reader) (string, string) {
	raw, err := io.ReadAll(io.LimitReader(body, 32<<10))
	if err != nil || len(raw) == 0 {
		return "", ""
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", safeUpstreamHint(raw)
	}
	var code, message string
	if nested, ok := payload["error"].(map[string]any); ok {
		code = firstString(nested, "code", "type")
		message = firstString(nested, "message", "detail")
	} else if value, ok := payload["error"].(string); ok {
		message = value
	}
	if code == "" {
		code = firstString(payload, "code", "type")
	}
	if message == "" {
		message = firstString(payload, "message", "detail")
	}
	if code == "" && message == "" {
		message = safeUpstreamHint(raw)
	}
	code = security.Redact(strings.TrimSpace(code))
	message = security.Redact(strings.TrimSpace(message))
	if len(code) > 120 {
		code = code[:120]
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return code, message
}

func safeUpstreamHint(raw []byte) string {
	value := strings.ToLower(string(raw))
	for _, needle := range []string{"usage_limit", "usage limit", "hit your usage", "purchase more credits"} {
		if strings.Contains(value, needle) {
			return "usage_limit"
		}
	}
	for _, needle := range []string{
		"text.format", "response_format", "json_schema", "schema",
		"max_output_tokens", "tools", "include",
	} {
		if strings.Contains(value, needle) {
			return needle
		}
	}
	return ""
}

func isUsageLimitRejection(code, message string) bool {
	value := strings.ToLower(code + " " + message)
	return strings.Contains(value, "usage_limit") || strings.Contains(value, "usage limit")
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func upstreamErrorDetail(code, message string) string {
	if code == "" {
		return message
	}
	if message == "" {
		return code
	}
	return code + ": " + message
}

func ResolveCodexResponsesURL(baseURL string, responsesPath string) string {
	if responsesPath == "" {
		responsesPath = "/codex/responses"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + responsesPath
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	responsePath := "/" + strings.TrimLeft(responsesPath, "/")
	if strings.HasSuffix(basePath, "/codex") && strings.HasPrefix(responsePath, "/codex/") {
		responsePath = strings.TrimPrefix(responsePath, "/codex")
	}
	parsed.Path = basePath + responsePath
	return parsed.String()
}

func ResolveCodexModelsURL(baseURL, modelsPath, clientVersion string) string {
	if modelsPath == "" {
		modelsPath = "/codex/models"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + modelsPath
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	modelPath := "/" + strings.TrimLeft(modelsPath, "/")
	if strings.HasSuffix(basePath, "/codex") && strings.HasPrefix(modelPath, "/codex/") {
		modelPath = strings.TrimPrefix(modelPath, "/codex")
	}
	parsed.Path = basePath + modelPath
	query := parsed.Query()
	if clientVersion != "" {
		query.Set("client_version", clientVersion)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (t *Transport) defaults(options CodexTransportOptions) CodexTransportOptions {
	if options.TimeoutMs == 0 {
		options.TimeoutMs = t.cfg.RequestTimeoutMs
	}
	if options.TimeoutMs < 1000 {
		options.TimeoutMs = 1000
	}
	if options.TimeoutMs > 300000 {
		options.TimeoutMs = 300000
	}
	if options.MaxRetries == 0 {
		options.MaxRetries = t.cfg.MaxRetries
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	if options.MaxRetries > 10 {
		options.MaxRetries = 10
	}
	return options
}

func validateRequest(request CodexResponseRequest) error {
	if err := llmcontract.ValidateOutputContract(request.OutputContract); err != nil {
		return err
	}
	if request.Model == "" || len(request.Model) > 120 {
		return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "model Codex invalido", http.StatusBadRequest), "model", "obrigatorio e ate 120 caracteres")
	}
	if len(request.Intelligence) > 120 {
		return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "intelligence Codex invalido", http.StatusBadRequest), "intelligence", "ate 120 caracteres")
	}
	if len(request.Input) == 0 || len(request.Input) > 500 {
		return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "input Codex invalido", http.StatusBadRequest), "input", "1..500 itens")
	}
	for _, item := range request.Input {
		if item.Role != "user" && item.Role != "assistant" && item.Role != "system" && item.Role != "developer" {
			return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "role Codex invalido", http.StatusBadRequest), "input.role", "user, assistant, system ou developer")
		}
		if item.Content == "" || len(item.Content) > 512*1024 {
			return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "content Codex invalido", http.StatusBadRequest), "input.content", "obrigatorio e ate 512 KB")
		}
	}
	if len(request.Instructions) > 128*1024 {
		return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "instructions grande demais", http.StatusBadRequest), "instructions", "ate 128 KB")
	}
	if len(request.Tools) > 100 {
		return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "tools demais", http.StatusBadRequest), "tools", "ate 100 itens")
	}
	if request.Reasoning != nil {
		if len(request.Reasoning.Effort) > 80 {
			return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "reasoning effort invalido", http.StatusBadRequest), "reasoning.effort", "ate 80 caracteres")
		}
		if len(request.Reasoning.Summary) > 80 {
			return security.WithDetail(security.NewError("ERR_CODEX_REQUEST_INVALID", "reasoning summary invalido", http.StatusBadRequest), "reasoning.summary", "ate 80 caracteres")
		}
	}
	return nil
}

func parseResponse(resp *http.Response) (*CodexResponseStream, bool, time.Duration, error) {
	raw, err := readLimitedResponse(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, false, 0, err
	}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") || looksLikeSSE(raw) {
		events, err := ParseSSE(bytes.NewReader(raw), 1<<20)
		if err != nil {
			return nil, false, 0, err
		}
		return &CodexResponseStream{Events: events, OutputText: collectOutputText(events)}, false, 0, nil
	}
	var parsed struct {
		ID     string     `json:"id"`
		Usage  CodexUsage `json:"usage"`
		Type   string     `json:"type"`
		Output any        `json:"output"`
	}
	_ = json.Unmarshal(raw, &parsed)
	eventType := parsed.Type
	if eventType == "" {
		eventType = "response"
	}
	return &CodexResponseStream{
		Events:     []CodexStreamEvent{{Type: eventType, Payload: string(raw)}},
		ResponseID: parsed.ID,
		Usage:      parsed.Usage,
		OutputText: outputTextFromJSON(raw),
	}, false, 0, nil
}

func readLimitedResponse(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, security.Wrap("ERR_CODEX_STREAM_INVALID", "falha ao ler resposta Codex", http.StatusBadGateway, err)
	}
	if int64(len(raw)) > limit {
		return nil, security.NewError("ERR_CODEX_STREAM_INVALID", "resposta Codex excede limite", http.StatusBadGateway)
	}
	return raw, nil
}

func looksLikeSSE(raw []byte) bool {
	text := strings.TrimSpace(string(raw))
	return strings.Contains(text, "\ndata:") && (strings.HasPrefix(text, "event:") || strings.Contains(text, "\nevent:"))
}

func collectOutputText(events []CodexStreamEvent) string {
	var out strings.Builder
	for _, event := range events {
		if event.Type != "response.output_text.delta" {
			continue
		}
		if event.Payload == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if delta, ok := payload["delta"].(string); ok {
			out.WriteString(delta)
			continue
		}
		if text, ok := payload["output_text"].(string); ok {
			out.WriteString(text)
			continue
		}
		if text, ok := payload["text"].(string); ok && event.Type == "response.output_text.delta" {
			out.WriteString(text)
		}
	}
	if out.Len() > 0 {
		return out.String()
	}
	for _, event := range events {
		if event.Type != "response.output_text.done" || event.Payload == "" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(event.Payload), &payload) == nil {
			if text := directOutputText(payload); text != "" {
				return text
			}
		}
	}
	for _, event := range events {
		if event.Type != "response.completed" || event.Payload == "" {
			continue
		}
		if text := outputTextFromJSON([]byte(event.Payload)); text != "" {
			return text
		}
	}
	return ""
}

func outputTextFromJSON(raw []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return outputTextFromPayload(payload)
}

func outputTextFromPayload(payload map[string]any) string {
	if text := directOutputText(payload); text != "" {
		return text
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if text := outputTextFromPayload(response); text != "" {
			return text
		}
	}
	output, _ := payload["output"].([]any)
	for _, itemValue := range output {
		item, _ := itemValue.(map[string]any)
		content, _ := item["content"].([]any)
		for _, contentValue := range content {
			part, _ := contentValue.(map[string]any)
			if text := directOutputText(part); text != "" {
				return text
			}
		}
	}
	return ""
}

func directOutputText(payload map[string]any) string {
	for _, key := range []string{"delta", "text", "output_text", "outputText"} {
		if text, ok := payload[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func retryDelay(resp *http.Response, fallbackMs int) time.Duration {
	if value := resp.Header.Get("retry-after-ms"); value != "" {
		if ms, err := strconv.Atoi(value); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	if value := resp.Header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Duration(fallbackMs) * time.Millisecond
}

func mapContextErr(err error) error {
	if err == context.DeadlineExceeded {
		return security.Wrap("ERR_CODEX_TIMEOUT", "timeout ao chamar Codex", http.StatusGatewayTimeout, err)
	}
	return security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado", http.StatusRequestTimeout, err)
}
