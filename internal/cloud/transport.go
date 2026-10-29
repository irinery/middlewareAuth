package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/llmcontract"
	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	Anthropic  = "anthropic"
	OpenRouter = "openrouter"
	apiVersion = "2026-10-21"
	maxBody    = 5 << 20
)

// Endpoints are fixed by the server. They are injectable only for local tests.
type Transport struct {
	Client        *http.Client
	AnthropicURL  string
	OpenRouterURL string
}

func NewTransport(client *http.Client) *Transport {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Transport{Client: &copy, AnthropicURL: "https://api.anthropic.com", OpenRouterURL: "https://openrouter.ai"}
}

func (t *Transport) endpoint(provider, path string) string {
	if provider == Anthropic {
		return strings.TrimRight(t.AnthropicURL, "/") + path
	}
	return strings.TrimRight(t.OpenRouterURL, "/") + path
}

func (t *Transport) request(ctx context.Context, provider, key, workspaceID, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "payload do provider invalido", http.StatusBadRequest)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.endpoint(provider, path), reader)
	if err != nil {
		return nil, security.NewError("ERR_CLOUD_PROVIDER_FAILED", "falha ao montar request", http.StatusBadGateway)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json, text/event-stream")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if provider == Anthropic {
		req.Header.Set("anthropic-version", apiVersion)
		if workspaceID != "" {
			req.Header.Set("anthropic-workspace-id", workspaceID)
		}
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, security.NewError("ERR_CONTEXT_CANCELLED", "request cancelado", http.StatusRequestTimeout)
		}
		var netErr net.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
			return nil, security.NewError("ERR_CLOUD_TIMEOUT", "timeout do provider", http.StatusGatewayTimeout)
		}
		return nil, security.NewError("ERR_CLOUD_PROVIDER_FAILED", "provider indisponivel", http.StatusBadGateway)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return nil, security.NewError("ERR_CLOUD_RESPONSE_INVALID", "resposta do provider invalida", http.StatusBadGateway)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp.StatusCode)
	}
	return raw, nil
}

func statusError(status int) error {
	switch status {
	case 401, 403:
		return security.NewError("ERR_CLOUD_AUTH_REJECTED", "provider recusou credencial", http.StatusUnauthorized)
	case 408, 504:
		return security.NewError("ERR_CLOUD_TIMEOUT", "timeout do provider", http.StatusGatewayTimeout)
	case 429:
		return security.NewError("ERR_CLOUD_RATE_LIMITED", "provider aplicou rate limit", http.StatusTooManyRequests)
	}
	if status >= 400 && status < 500 {
		return security.NewError("ERR_CLOUD_REQUEST_INVALID", "provider recusou request", http.StatusBadRequest)
	}
	return security.NewError("ERR_CLOUD_PROVIDER_FAILED", "provider indisponivel", http.StatusBadGateway)
}

func (t *Transport) VerifyKey(ctx context.Context, provider, key, workspaceID string) error {
	if strings.TrimSpace(key) != key || key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
		return security.NewError("ERR_CLOUD_REQUEST_INVALID", "apiKey invalida", http.StatusBadRequest)
	}
	path := "/api/v1/key"
	if provider == Anthropic {
		path = "/v1/models?limit=1"
	}
	raw, err := t.request(ctx, provider, key, workspaceID, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal(raw, &parsed) != nil || parsed["data"] == nil {
		return security.NewError("ERR_CLOUD_RESPONSE_INVALID", "resposta de autenticacao invalida", http.StatusBadGateway)
	}
	return nil
}

func (t *Transport) Send(ctx context.Context, provider, key, workspaceID string, request codex.CodexResponseRequest) (*codex.CodexResponseStream, error) {
	if strings.TrimSpace(request.Model) == "" || len(request.Input) == 0 {
		return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "model e input obrigatorios", http.StatusBadRequest)
	}
	if err := llmcontract.ValidateOutputContract(request.OutputContract); err != nil {
		return nil, err
	}
	maxTokens := 1024
	if value, ok := request.Extra["max_output_tokens"]; ok {
		n, valid := value.(float64)
		if !valid || n < 1 || n > 128000 || n != float64(int(n)) {
			return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "max_output_tokens invalido", http.StatusBadRequest)
		}
		maxTokens = int(n)
	}
	var body map[string]any
	path := "/v1/messages"
	if provider == Anthropic {
		messages := make([]map[string]string, 0, len(request.Input))
		systems := []string{}
		if request.Instructions != "" {
			systems = append(systems, request.Instructions)
		}
		for _, item := range request.Input {
			if item.Role == "system" || item.Role == "developer" {
				systems = append(systems, item.Content)
				continue
			}
			if item.Role != "user" && item.Role != "assistant" {
				return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "role invalida", http.StatusBadRequest)
			}
			messages = append(messages, map[string]string{"role": item.Role, "content": item.Content})
		}
		if len(messages) == 0 {
			return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "mensagem de usuario obrigatoria", http.StatusBadRequest)
		}
		body = map[string]any{"model": request.Model, "messages": messages, "max_tokens": maxTokens, "stream": request.Stream}
		if len(systems) > 0 {
			body["system"] = strings.Join(systems, "\n\n")
		}
		outputConfig := map[string]any{}
		if request.Reasoning != nil && request.Reasoning.Effort != "" {
			outputConfig["effort"] = request.Reasoning.Effort
		}
		if request.OutputContract != nil {
			schema, err := llmcontract.ProviderJSONSchema(request.OutputContract)
			if err != nil {
				return nil, err
			}
			outputConfig["format"] = map[string]any{"type": "json_schema", "schema": schema}
		}
		if len(outputConfig) > 0 {
			body["output_config"] = outputConfig
		}
	} else {
		path = "/api/v1/chat/completions"
		messages := make([]map[string]string, 0, len(request.Input)+1)
		if request.Instructions != "" {
			messages = append(messages, map[string]string{"role": "system", "content": request.Instructions})
		}
		for _, item := range request.Input {
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			if role != "system" && role != "user" && role != "assistant" {
				return nil, security.NewError("ERR_CLOUD_REQUEST_INVALID", "role invalida", http.StatusBadRequest)
			}
			messages = append(messages, map[string]string{"role": role, "content": item.Content})
		}
		body = map[string]any{"model": request.Model, "messages": messages, "max_tokens": maxTokens, "stream": request.Stream}
		if request.OutputContract != nil {
			schema, err := llmcontract.ProviderJSONSchema(request.OutputContract)
			if err != nil {
				return nil, err
			}
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": llmcontract.ProviderSchemaName(request.OutputContract), "strict": true, "schema": schema}}
		}
	}
	raw, err := t.request(ctx, provider, key, workspaceID, http.MethodPost, path, body)
	if err != nil {
		if request.OutputContract != nil && security.Code(err) == "ERR_CLOUD_REQUEST_INVALID" {
			return nil, llmcontract.UnsupportedOutputContract()
		}
		return nil, err
	}
	var result *codex.CodexResponseStream
	if request.Stream {
		result, err = parseStream(provider, raw)
	} else if provider == Anthropic {
		result, err = parseAnthropic(raw)
	} else {
		result, err = parseOpenRouter(raw)
	}
	if err != nil {
		return nil, err
	}
	if request.OutputContract != nil {
		result.OutputText, err = llmcontract.NormalizeStructuredOutputText(result.OutputText)
		if err != nil {
			return nil, err
		}
	}
	if result.OutputText == "" {
		return nil, security.NewError("ERR_CLOUD_RESPONSE_EMPTY", "provider retornou resposta vazia", http.StatusBadGateway)
	}
	return result, nil
}

func result(id, output string, inputTokens, outputTokens int) *codex.CodexResponseStream {
	payload, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
	return &codex.CodexResponseStream{ResponseID: id, OutputText: output, Usage: codex.CodexUsage{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: inputTokens + outputTokens}, Events: []codex.CodexStreamEvent{{Type: "response.output_text.delta", Payload: string(payload)}, {Type: "done"}}}
}

func parseAnthropic(raw []byte) (*codex.CodexResponseStream, error) {
	var data struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return nil, security.NewError("ERR_CLOUD_RESPONSE_INVALID", "resposta Claude invalida", http.StatusBadGateway)
	}
	var output strings.Builder
	for _, block := range data.Content {
		if block.Type == "text" {
			output.WriteString(block.Text)
		}
	}
	return result(data.ID, output.String(), data.Usage.Input, data.Usage.Output), nil
}

func parseOpenRouter(raw []byte) (*codex.CodexResponseStream, error) {
	var data struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &data) != nil || len(data.Choices) == 0 {
		return nil, security.NewError("ERR_CLOUD_RESPONSE_INVALID", "resposta OpenRouter invalida", http.StatusBadGateway)
	}
	return result(data.ID, data.Choices[0].Message.Content, data.Usage.Input, data.Usage.Output), nil
}

func parseStream(provider string, raw []byte) (*codex.CodexResponseStream, error) {
	events, err := codex.ParseSSE(bytes.NewReader(raw), 1<<20)
	if err != nil {
		return nil, security.NewError("ERR_CLOUD_RESPONSE_INVALID", "stream invalido", http.StatusBadGateway)
	}
	var output strings.Builder
	var id string
	var inputTokens, outputTokens int
	for _, event := range events {
		if event.Type == "done" {
			continue
		}
		if event.Type == "error" || bytes.Contains([]byte(event.Payload), []byte(`"error":`)) {
			return nil, security.NewError("ERR_CLOUD_PROVIDER_FAILED", "provider interrompeu stream", http.StatusBadGateway)
		}
		if provider == Anthropic {
			var data struct {
				Type    string `json:"type"`
				Message struct {
					ID    string `json:"id"`
					Usage struct {
						Input  int `json:"input_tokens"`
						Output int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Usage struct {
					Output int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(event.Payload), &data) != nil {
				continue
			}
			if data.Type == "message_start" {
				id = data.Message.ID
				inputTokens = data.Message.Usage.Input
				outputTokens = data.Message.Usage.Output
			}
			if data.Type == "content_block_delta" && data.Delta.Type == "text_delta" {
				output.WriteString(data.Delta.Text)
			}
			if data.Type == "message_delta" {
				outputTokens = data.Usage.Output
			}
		} else {
			var data struct {
				ID      string `json:"id"`
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					Input  int `json:"prompt_tokens"`
					Output int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(event.Payload), &data) != nil {
				continue
			}
			if data.ID != "" {
				id = data.ID
			}
			if len(data.Choices) > 0 {
				output.WriteString(data.Choices[0].Delta.Content)
			}
			if data.Usage != nil {
				inputTokens = data.Usage.Input
				outputTokens = data.Usage.Output
			}
		}
	}
	return result(id, output.String(), inputTokens, outputTokens), nil
}
