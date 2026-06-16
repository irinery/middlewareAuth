package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/security"
)

type Model struct {
	ID string `json:"id"`
}

type Transport struct {
	client *http.Client
}

func NewTransport(client *http.Client) *Transport {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Transport{client: client}
}

func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio invalida", http.StatusBadRequest)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio precisa ser http ou https", http.StatusBadRequest)
	}
	if parsed.User != nil {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio nao pode conter userinfo", http.StatusBadRequest)
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return security.NewError("ERR_LMSTUDIO_BASE_URL_INVALID", "baseUrl LM Studio precisa apontar para host local ou privado", http.StatusBadRequest)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
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
	if strings.TrimSpace(apiKey) == "" {
		return nil, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeBaseURL(baseURL)+"/v1/models", nil)
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha ao montar request LM Studio", http.StatusInternalServerError, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha HTTP ao chamar LM Studio", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "LM Studio recusou autenticacao/listagem: "+security.Redact(string(raw)), http.StatusBadGateway, nil)
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
	if strings.TrimSpace(apiKey) == "" {
		return nil, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest)
	}
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
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "falha HTTP ao chamar LM Studio", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return nil, security.Wrap("ERR_LMSTUDIO_HTTP_FAILED", "LM Studio retornou erro: "+security.Redact(string(rawResp)), http.StatusBadGateway, nil)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return parseChatStream(resp.Body)
	}
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, security.Wrap("ERR_LMSTUDIO_RESPONSE_INVALID", "falha ao ler resposta LM Studio", http.StatusBadGateway, err)
	}
	return parseChatCompletion(rawResp)
}

func chatCompletionBody(request codex.CodexResponseRequest) (map[string]any, error) {
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
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
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
	return &codex.CodexResponseStream{Events: events, OutputText: output.String()}, nil
}
