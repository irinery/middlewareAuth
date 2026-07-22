package client

import (
	"encoding/json"
	"net/http"
)

type ClientOptions struct {
	BaseURL         string
	MiddlewareToken string
	TimeoutMs       int
	ProjectID       string
	HTTPClient      *http.Client
}

type StartLoginRequest struct {
	ProjectID string `json:"-"`
	ProfileID string `json:"profileId,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

type LoginStartResponse struct {
	LoginSessionID  string `json:"loginSessionId"`
	AuthURL         string `json:"authUrl,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	ExpiresAt       int64  `json:"expiresAt"`
}

type RefreshRequest struct {
	ProjectID string
	ProfileID string
}

type CodexResponseRequest struct {
	Model          string                `json:"model"`
	Intelligence   string                `json:"intelligence,omitempty"`
	Instructions   string                `json:"instructions,omitempty"`
	Input          []CodexInputItem      `json:"input"`
	Stream         bool                  `json:"stream"`
	Store          bool                  `json:"store"`
	Reasoning      *CodexReasoningConfig `json:"reasoning,omitempty"`
	Tools          []CodexToolDefinition `json:"tools,omitempty"`
	OutputContract *OutputContract       `json:"outputContract,omitempty"`
	Extra          map[string]any        `json:"-"`
}

type OutputContract struct {
	ID         string          `json:"id"`
	SchemaHash string          `json:"schemaHash"`
	Strict     bool            `json:"strict"`
	JSONSchema json.RawMessage `json:"jsonSchema"`
}

type CodexInputItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CodexReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type CodexToolDefinition struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CodexStreamEvent struct {
	Type    string `json:"type"`
	Payload string `json:"payload,omitempty"`
}

type CodexUsage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

type CodexResponseStream struct {
	Events     []CodexStreamEvent `json:"events"`
	ResponseID string             `json:"responseId,omitempty"`
	Usage      CodexUsage         `json:"usage,omitempty"`
	OutputText string             `json:"outputText,omitempty"`
}

type ClientError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Status       int    `json:"status,omitempty"`
	RetryAfterMs int    `json:"retryAfterMs,omitempty"`
}

func (e *ClientError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func (r CodexResponseRequest) MarshalJSON() ([]byte, error) {
	type alias CodexResponseRequest
	raw, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	delete(out, "Extra")
	for key, value := range r.Extra {
		if _, protected := out[key]; protected {
			continue
		}
		out[key] = value
	}
	return json.Marshal(out)
}
