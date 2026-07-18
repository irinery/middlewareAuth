package codex

import "encoding/json"

type CodexResponseRequest struct {
	Model        string                `json:"model"`
	Intelligence string                `json:"intelligence,omitempty"`
	Instructions string                `json:"instructions,omitempty"`
	Input        []CodexInputItem      `json:"input"`
	Stream       bool                  `json:"stream"`
	Store        bool                  `json:"store"`
	Reasoning    *CodexReasoningConfig `json:"reasoning,omitempty"`
	Tools        []CodexToolDefinition `json:"tools,omitempty"`
	Extra        map[string]any        `json:"-"`
}

type CodexInputItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CodexReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type CodexReasoningEffortOption struct {
	Effort      string `json:"effort"`
	Description string `json:"description,omitempty"`
}

type CodexServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CodexModelInfo struct {
	Slug                     string                       `json:"slug"`
	DisplayName              string                       `json:"display_name"`
	Description              string                       `json:"description,omitempty"`
	DefaultReasoningLevel    string                       `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels []CodexReasoningEffortOption `json:"supported_reasoning_levels"`
	Visibility               string                       `json:"visibility"`
	SupportedInAPI           bool                         `json:"supported_in_api"`
	Priority                 int                          `json:"priority"`
	ServiceTiers             []CodexServiceTier           `json:"service_tiers,omitempty"`
	DefaultServiceTier       string                       `json:"default_service_tier,omitempty"`
}

type CodexModelsResponse struct {
	Models []CodexModelInfo `json:"models"`
}

type CodexToolDefinition struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CodexTransportOptions struct {
	TimeoutMs         int
	MaxRetries        int
	SessionID         string
	AdditionalHeaders []HeaderPair
}

type HeaderPair struct {
	Key   string
	Value string
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

func (r *CodexResponseRequest) UnmarshalJSON(data []byte) error {
	type alias CodexResponseRequest
	var known alias
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"model", "intelligence", "instructions", "input", "stream", "store", "reasoning", "tools"} {
		delete(raw, key)
	}
	*r = CodexResponseRequest(known)
	if len(raw) > 0 {
		r.Extra = raw
	}
	return nil
}
