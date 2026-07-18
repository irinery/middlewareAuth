package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/config"
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
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, security.Wrap("ERR_CODEX_REQUEST_INVALID", "payload Codex invalido", http.StatusBadRequest, err)
	}
	if len(raw) > 2<<20 {
		return nil, security.NewError("ERR_CODEX_REQUEST_INVALID", "payload Codex excede 2 MB", http.StatusBadRequest)
	}

	var lastErr error
	for attempt := 0; attempt <= options.MaxRetries; attempt++ {
		response, retry, delay, err := t.sendOnce(ctx, credential, raw, options)
		if err == nil {
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

func (t *Transport) sendOnce(ctx context.Context, credential auth.StoredOAuthCredential, raw []byte, options CodexTransportOptions) (*CodexResponseStream, bool, time.Duration, error) {
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
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
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
				return nil, false, 0, security.NewError("ERR_CODEX_REQUEST_INVALID", "Codex recusou o request", http.StatusBadRequest)
			}
			return nil, false, 0, security.NewError("ERR_CODEX_HTTP_FAILED", "Codex retornou status inesperado", http.StatusBadGateway)
		}
	}
	return parseResponse(resp)
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
	return out.String()
}

func outputTextFromJSON(raw []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if text, ok := payload["outputText"].(string); ok {
		return text
	}
	if text, ok := payload["output_text"].(string); ok {
		return text
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
