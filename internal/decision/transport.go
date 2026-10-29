package decision

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

	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	TypeSafe   = "typesafe-jev"
	OpenRouter = "openrouter-jev"
	maxBody    = 5 << 20
)

type Request struct {
	Model     string         `json:"model,omitempty"`
	State     any            `json:"state"`
	Questions map[string]any `json:"questions"`
}

type Response struct {
	ProviderID string          `json:"providerId"`
	ProjectID  string          `json:"projectId"`
	ProfileID  string          `json:"profileId"`
	Model      string          `json:"model"`
	ID         string          `json:"id,omitempty"`
	Answers    json.RawMessage `json:"answers"`
	Usage      json.RawMessage `json:"usage,omitempty"`
}

type Transport struct {
	Client        *http.Client
	TypeSafeURL   string
	OpenRouterURL string
}

func NewTransport(client *http.Client) *Transport {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Transport{Client: &copy, TypeSafeURL: "https://api.typesafe.ai", OpenRouterURL: "https://openrouter.ai"}
}

func (t *Transport) endpoint(provider string) (string, bool) {
	switch provider {
	case TypeSafe:
		return strings.TrimRight(t.TypeSafeURL, "/"), true
	case OpenRouter:
		return strings.TrimRight(t.OpenRouterURL, "/"), true
	default:
		return "", false
	}
}

func (t *Transport) request(ctx context.Context, provider, key, method, path string, body any) ([]byte, error) {
	base, ok := t.endpoint(provider)
	if !ok {
		return nil, security.NewError("ERR_DECISION_PROVIDER_UNKNOWN", "provider de decisao desconhecido", http.StatusBadRequest)
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, security.NewError("ERR_DECISION_REQUEST_INVALID", "payload de decisao invalido", http.StatusBadRequest)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, security.NewError("ERR_DECISION_PROVIDER_FAILED", "falha ao montar request", http.StatusBadGateway)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, security.NewError("ERR_CONTEXT_CANCELLED", "request cancelado", http.StatusRequestTimeout)
		}
		var netErr net.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
			return nil, security.NewError("ERR_DECISION_TIMEOUT", "timeout do provider", http.StatusGatewayTimeout)
		}
		return nil, security.NewError("ERR_DECISION_PROVIDER_FAILED", "provider indisponivel", http.StatusBadGateway)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return nil, security.NewError("ERR_DECISION_RESPONSE_INVALID", "resposta do provider invalida", http.StatusBadGateway)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		switch resp.StatusCode {
		case 401, 403:
			return nil, security.NewError("ERR_DECISION_AUTH_REJECTED", "provider recusou credencial", http.StatusUnauthorized)
		case 408, 504, 529:
			return nil, security.NewError("ERR_DECISION_TIMEOUT", "provider indisponivel ou sobrecarregado", http.StatusGatewayTimeout)
		case 429:
			return nil, security.NewError("ERR_DECISION_RATE_LIMITED", "provider aplicou rate limit", http.StatusTooManyRequests)
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, security.NewError("ERR_DECISION_REQUEST_INVALID", "provider recusou request", http.StatusBadRequest)
		}
		return nil, security.NewError("ERR_DECISION_PROVIDER_FAILED", "provider indisponivel", http.StatusBadGateway)
	}
	return raw, nil
}

func (t *Transport) VerifyKey(ctx context.Context, provider, key string) error {
	if strings.TrimSpace(key) != key || key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
		return security.NewError("ERR_DECISION_REQUEST_INVALID", "apiKey invalida", http.StatusBadRequest)
	}
	path := "/api/v1/key"
	if provider == TypeSafe {
		path = "/v1/models"
	}
	raw, err := t.request(ctx, provider, key, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal(raw, &parsed) != nil {
		return security.NewError("ERR_DECISION_RESPONSE_INVALID", "resposta de autenticacao invalida", http.StatusBadGateway)
	}
	if provider == TypeSafe {
		var response struct {
			Models []json.RawMessage `json:"models"`
		}
		if json.Unmarshal(raw, &response) != nil || len(response.Models) == 0 {
			return security.NewError("ERR_DECISION_RESPONSE_INVALID", "resposta de autenticacao invalida", http.StatusBadGateway)
		}
		return nil
	}
	if parsed["data"] == nil {
		return security.NewError("ERR_DECISION_RESPONSE_INVALID", "resposta de autenticacao invalida", http.StatusBadGateway)
	}
	return nil
}

func (t *Transport) Evaluate(ctx context.Context, provider, key string, input Request) (*Response, error) {
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" || input.State == nil || len(input.Questions) == 0 || len(input.Questions) > 100 {
		return nil, security.NewError("ERR_DECISION_REQUEST_INVALID", "model, state e questions obrigatorios", http.StatusBadRequest)
	}
	for id, question := range input.Questions {
		if strings.TrimSpace(id) == "" || len(id) > 128 || question == nil {
			return nil, security.NewError("ERR_DECISION_REQUEST_INVALID", "question invalida", http.StatusBadRequest)
		}
	}
	path := "/api/alpha/decisions"
	if provider == TypeSafe {
		path = "/v1/systemone"
	}
	raw, err := t.request(ctx, provider, key, http.MethodPost, path, input)
	if err != nil {
		return nil, err
	}
	var upstream struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
		Usage   json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(raw, &upstream) != nil || strings.TrimSpace(upstream.Model) == "" || len(upstream.Answers) == 0 || string(upstream.Answers) == "null" {
		return nil, security.NewError("ERR_DECISION_RESPONSE_INVALID", "resposta de decisao invalida", http.StatusBadGateway)
	}
	return &Response{Model: upstream.Model, ID: upstream.ID, Answers: upstream.Answers, Usage: upstream.Usage}, nil
}
