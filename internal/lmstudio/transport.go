package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
)

type Model struct {
	ID string `json:"id"`
}

type Transport struct {
	client *http.Client
}

const (
	maxBaseURLBytes  = 2048
	maxAPIKeyBytes   = 4096
	maxModelsBytes   = 2 << 20
	maxResponseBytes = 5 << 20
)

func NewTransport(client *http.Client) *Transport {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Timeout: 60 * time.Second, Transport: transport}
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Transport{client: &copy}
}

func ValidateBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxBaseURLBytes {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio invalida", http.StatusBadRequest)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio invalida", http.StatusBadRequest)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio precisa ser http ou https", http.StatusBadRequest)
	}
	if parsed.User != nil {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio nao pode conter userinfo", http.StatusBadRequest)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio nao pode conter query ou fragmento", http.StatusBadRequest)
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "porta LM Studio invalida", http.StatusBadRequest)
	}
	if port := parsed.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "porta LM Studio invalida", http.StatusBadRequest)
		}
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio precisa apontar para host local ou privado", http.StatusBadRequest)
	}
	if !ip.IsUnspecified() && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return nil
	}
	return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio fora de rede local/privada", http.StatusBadRequest)
}

func NormalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func AccountID(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "lmstudio:local"
	}
	return "lmstudio:" + parsed.Host
}

func (t *Transport) ListModels(ctx context.Context, baseURL string, apiKey string) ([]Model, error) {
	if err := ValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if !validAPIKey(apiKey) {
		return nil, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest)
	}
	apiKey = strings.TrimSpace(apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeBaseURL(baseURL)+"/v1/models", nil)
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha ao montar request LM Studio", http.StatusInternalServerError, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, mapTransportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := readLimited(resp.Body, maxModelsBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, mapHTTPStatus(resp.StatusCode)
	}
	var parsed struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_RESPONSE_INVALID", "resposta de modelos LM Studio invalida", http.StatusBadGateway, err)
	}
	return parsed.Data, nil
}

func (t *Transport) SendResponse(ctx context.Context, baseURL string, apiKey string, request codex.CodexResponseRequest) (*codex.CodexResponseStream, error) {
	if err := ValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if !validAPIKey(apiKey) {
		return nil, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest)
	}
	apiKey = strings.TrimSpace(apiKey)
	body, err := chatCompletionBody(request)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_REQUEST_INVALID", "payload LM Studio invalido", http.StatusBadRequest, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NormalizeBaseURL(baseURL)+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha ao montar request LM Studio", http.StatusInternalServerError, err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, mapTransportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
		if request.OutputContract != nil && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity) {
			return nil, llmcontract.UnsupportedOutputContract()
		}
		return nil, mapHTTPStatus(resp.StatusCode)
	}
	rawResp, err := readLimited(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	var response *codex.CodexResponseStream
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || looksLikeSSE(rawResp) {
		response, err = parseChatStream(bytes.NewReader(rawResp))
	} else {
		response, err = parseChatCompletion(rawResp)
	}
	if err != nil {
		return nil, err
	}
	if request.OutputContract != nil {
		response.OutputText, err = llmcontract.NormalizeStructuredOutputText(response.OutputText)
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}

func validAPIKey(apiKey string) bool {
	apiKey = strings.TrimSpace(apiKey)
	return apiKey != "" && len(apiKey) <= maxAPIKeyBytes
}

func mapTransportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return security.Wrap("ERR_CONTEXT_CANCELLED", "request LM Studio cancelado", http.StatusRequestTimeout, err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return security.Wrap("ERR_LMSTUDIO_TIMEOUT", "timeout ao chamar LM Studio", http.StatusGatewayTimeout, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return security.Wrap("ERR_LMSTUDIO_TIMEOUT", "timeout ao chamar LM Studio", http.StatusGatewayTimeout, err)
	}
	return security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha HTTP ao chamar LM Studio", http.StatusBadGateway, err)
}

func mapHTTPStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return security.NewError("ERR_LMSTUDIO_AUTH_REJECTED", "LM Studio recusou autenticacao", http.StatusUnauthorized)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return security.NewError("ERR_LMSTUDIO_TIMEOUT", "timeout ao chamar LM Studio", http.StatusGatewayTimeout)
	case http.StatusTooManyRequests:
		return security.NewError("ERR_LMSTUDIO_RATE_LIMITED", "LM Studio aplicou rate limit", http.StatusTooManyRequests)
	default:
		if status >= 400 && status < 500 {
			return security.NewError("ERR_LMSTUDIO_REQUEST_INVALID", "LM Studio recusou o request", http.StatusBadRequest)
		}
		return security.NewError("ERR_LMSTUDIO_HTTP_FAILED", "LM Studio indisponivel", http.StatusBadGateway)
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_RESPONSE_INVALID", "falha ao ler resposta LM Studio", http.StatusBadGateway, err)
	}
	if int64(len(raw)) > limit {
		return nil, security.NewError("ERR_LMSTUDIO_RESPONSE_INVALID", "resposta LM Studio excede limite", http.StatusBadGateway)
	}
	return raw, nil
}

func looksLikeSSE(raw []byte) bool {
	text := strings.TrimSpace(string(raw))
	return strings.Contains(text, "\ndata:") && (strings.HasPrefix(text, "event:") || strings.Contains(text, "\nevent:"))
}

func chatCompletionBody(request codex.CodexResponseRequest) (map[string]any, error) {
	if err := llmcontract.ValidateOutputContract(request.OutputContract); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, security.NewError("ERR_LMSTUDIO_REQUEST_INVALID", "model LM Studio obrigatorio", http.StatusBadRequest)
	}
	if len(request.Input) == 0 {
		return nil, security.NewError("ERR_LMSTUDIO_REQUEST_INVALID", "input LM Studio obrigatorio", http.StatusBadRequest)
	}
	messages := make([]map[string]string, 0, len(request.Input)+1)
	if request.Instructions != "" {
		messages = append(messages, map[string]string{"role": "system", "content": request.Instructions})
	}
	for _, item := range request.Input {
		role := item.Role
		if role == "developer" {
			role = "system"
		}
		if role == "" {
			role = "user"
		}
		messages = append(messages, map[string]string{"role": role, "content": item.Content})
	}
	body := map[string]any{
		"model":    request.Model,
		"messages": messages,
		"stream":   request.Stream,
	}
	if contract := request.OutputContract; contract != nil {
		providerSchema, err := llmcontract.ProviderJSONSchema(contract)
		if err != nil {
			return nil, err
		}
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   llmcontract.ProviderSchemaName(contract),
				"strict": contract.Strict,
				"schema": providerSchema,
			},
		}
	}
	for key, value := range request.Extra {
		if _, protected := body[key]; protected {
			continue
		}
		body[key] = value
	}
	return body, nil
}

func parseChatCompletion(raw []byte) (*codex.CodexResponseStream, error) {
	var parsed struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_RESPONSE_INVALID", "resposta LM Studio invalida", http.StatusBadGateway, err)
	}
	text := ""
	if len(parsed.Choices) > 0 {
		text = parsed.Choices[0].Message.Content
		if text == "" {
			text = parsed.Choices[0].Text
		}
	}
	eventPayload, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": text})
	return &codex.CodexResponseStream{
		Events: []codex.CodexStreamEvent{
			{Type: "response.output_text.delta", Payload: string(eventPayload)},
			{Type: "done"},
		},
		ResponseID: parsed.ID,
		Usage: codex.CodexUsage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
			TotalTokens:  parsed.Usage.TotalTokens,
		},
		OutputText: text,
	}, nil
}

func parseChatStream(reader io.Reader) (*codex.CodexResponseStream, error) {
	rawEvents, err := codex.ParseSSE(reader, 1<<20)
	if err != nil {
		return nil, err
	}
	var events []codex.CodexStreamEvent
	var output strings.Builder
	var responseID string
	var usage codex.CodexUsage
	for _, event := range rawEvents {
		if event.Type == "done" {
			events = append(events, event)
			continue
		}
		var payload struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if payload.ID != "" {
			responseID = payload.ID
		}
		if payload.Usage != nil {
			usage = codex.CodexUsage{
				InputTokens:  payload.Usage.PromptTokens,
				OutputTokens: payload.Usage.CompletionTokens,
				TotalTokens:  payload.Usage.TotalTokens,
			}
		}
		if len(payload.Choices) == 0 || payload.Choices[0].Delta.Content == "" {
			continue
		}
		delta := payload.Choices[0].Delta.Content
		output.WriteString(delta)
		eventPayload, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": delta})
		events = append(events, codex.CodexStreamEvent{Type: "response.output_text.delta", Payload: string(eventPayload)})
	}
	if len(events) == 0 || events[len(events)-1].Type != "done" {
		events = append(events, codex.CodexStreamEvent{Type: "done"})
	}
	return &codex.CodexResponseStream{Events: events, ResponseID: responseID, Usage: usage, OutputText: output.String()}, nil
}
