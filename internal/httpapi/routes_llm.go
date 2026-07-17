package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	providerOpenAI   = "openai"
	providerLMStudio = "lmstudio"
)

type LLMProviderCatalogResponse struct {
	ContractVersion string        `json:"contractVersion"`
	Providers       []LLMProvider `json:"providers"`
}

type LLMProvider struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	Auth         LLMProviderAuth         `json:"auth"`
	Defaults     LLMProviderDefaults     `json:"defaults"`
	Models       []LLMProviderModel      `json:"models"`
	Capabilities LLMProviderCapabilities `json:"capabilities"`
}

type LLMProviderAuth struct {
	Required    bool                   `json:"required"`
	Modes       []string               `json:"modes"`
	DefaultMode string                 `json:"defaultMode"`
	Fields      []LLMProviderAuthField `json:"fields"`
}

type LLMProviderAuthField struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
}

type LLMProviderDefaults struct {
	ProfileID string `json:"profileId"`
	Model     string `json:"model"`
}

type LLMProviderModel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type LLMProviderCapabilities struct {
	Stream             bool `json:"stream"`
	Refresh            bool `json:"refresh"`
	Intelligence       bool `json:"intelligence"`
	ReasoningEffort    bool `json:"reasoningEffort"`
	SystemInstructions bool `json:"systemInstructions"`
	Tools              bool `json:"tools"`
	Store              bool `json:"store"`
}

type llmLoginRequest struct {
	ProviderID string         `json:"providerId"`
	ProfileID  string         `json:"profileId"`
	Mode       string         `json:"mode"`
	AuthFields map[string]any `json:"authFields"`
}

type LLMLoginResponse struct {
	ProviderID      string             `json:"providerId"`
	ProjectID       string             `json:"projectId"`
	ProfileID       string             `json:"profileId"`
	LoginSessionID  string             `json:"loginSessionId"`
	Mode            string             `json:"mode"`
	Status          string             `json:"status"`
	Authenticated   bool               `json:"authenticated"`
	AuthURL         string             `json:"authUrl,omitempty"`
	VerificationURL string             `json:"verificationUrl,omitempty"`
	UserCode        string             `json:"userCode,omitempty"`
	ExpiresAt       int64              `json:"expiresAt,omitempty"`
	CompletedAt     int64              `json:"completedAt,omitempty"`
	BaseURL         string             `json:"baseUrl,omitempty"`
	AccountID       string             `json:"accountId,omitempty"`
	ModelCount      int                `json:"modelCount,omitempty"`
	SavedAt         int64              `json:"savedAt,omitempty"`
	Error           *security.AppError `json:"error,omitempty"`
}

type LLMStatusResponse struct {
	Authenticated bool   `json:"authenticated"`
	ProviderID    string `json:"providerId"`
	ProjectID     string `json:"projectId"`
	ProfileID     string `json:"profileId"`
	AccountID     string `json:"accountId,omitempty"`
	Email         string `json:"email,omitempty"`
	PlanType      string `json:"planType,omitempty"`
	BaseURL       string `json:"baseUrl,omitempty"`
	Expires       int64  `json:"expires,omitempty"`
}

type llmRefreshRequest struct {
	ProviderID string `json:"providerId"`
	ProfileID  string `json:"profileId"`
}

func (h *Handler) handleLLMProviders(w http.ResponseWriter, _ *http.Request, _ string) {
	writeJSON(w, http.StatusOK, LLMProviderCatalogResponse{
		ContractVersion: "middlewareauth.llm.v1",
		Providers: []LLMProvider{
			{
				ID:       providerOpenAI,
				Title:    "OpenAI",
				Auth:     LLMProviderAuth{Required: true, Modes: []string{"oauth", "device_code"}, DefaultMode: "device_code", Fields: []LLMProviderAuthField{}},
				Defaults: LLMProviderDefaults{ProfileID: "default", Model: "gpt-5.5"},
				Models: []LLMProviderModel{
					{ID: "gpt-5.5", Title: "gpt-5.5"},
					{ID: "gpt-5", Title: "gpt-5"},
				},
				Capabilities: LLMProviderCapabilities{Stream: true, Refresh: true, Intelligence: true, ReasoningEffort: true, SystemInstructions: true, Tools: false, Store: true},
			},
			{
				ID:    providerLMStudio,
				Title: "LM Studio",
				Auth: LLMProviderAuth{
					Required:    true,
					Modes:       []string{"api_key"},
					DefaultMode: "api_key",
					Fields: []LLMProviderAuthField{
						{ID: "baseUrl", Title: "Base URL", Type: "url", Required: true, Secret: false},
						{ID: "apiKey", Title: "API key", Type: "password", Required: true, Secret: true},
					},
				},
				Defaults:     LLMProviderDefaults{ProfileID: "default", Model: "local-model"},
				Models:       []LLMProviderModel{},
				Capabilities: LLMProviderCapabilities{Stream: true, Refresh: false, Intelligence: false, ReasoningEffort: false, SystemInstructions: true, Tools: false, Store: false},
			},
		},
	})
}

func (h *Handler) handleLLMLogin(w http.ResponseWriter, r *http.Request, projectID string) {
	var input llmLoginRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &input); err != nil {
		writeLLMError(w, err)
		return
	}
	providerID, profileID, err := normalizeLLMIdentity(input.ProviderID, input.ProfileID)
	if err != nil {
		writeLLMError(w, err)
		return
	}

	switch providerID {
	case providerOpenAI:
		mode := strings.TrimSpace(input.Mode)
		if mode == "" {
			mode = "device_code"
		}
		var started *LoginStartResponse
		if mode == "oauth" {
			started, err = h.startOAuthLogin(r.Context(), projectID, profileID)
		} else if mode == "device_code" {
			started, err = h.startDeviceCodeLogin(r.Context(), projectID, profileID)
		} else {
			err = security.NewError("ERR_LLM_REQUEST_INVALID", "modo de login nao suportado pelo provider", http.StatusBadRequest)
		}
		if err != nil {
			writeLLMError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, LLMLoginResponse{
			ProviderID:      providerID,
			ProjectID:       projectID,
			ProfileID:       profileID,
			LoginSessionID:  started.LoginSessionID,
			Mode:            mode,
			Status:          "pending",
			Authenticated:   false,
			AuthURL:         started.AuthURL,
			VerificationURL: started.VerificationURL,
			UserCode:        started.UserCode,
			ExpiresAt:       started.ExpiresAt,
		})
	case providerLMStudio:
		mode := strings.TrimSpace(input.Mode)
		if mode == "" {
			mode = "api_key"
		}
		if mode != "api_key" {
			writeLLMError(w, security.NewError("ERR_LLM_REQUEST_INVALID", "modo de login nao suportado pelo provider", http.StatusBadRequest))
			return
		}
		baseURL := stringLLMAuthField(input.AuthFields, "baseUrl")
		apiKey := stringLLMAuthField(input.AuthFields, "apiKey")
		configured, err := h.configureLMStudio(r.Context(), projectID, lmStudioAPIKeyRequest{
			ProfileID: profileID,
			BaseURL:   baseURL,
			APIKey:    apiKey,
		})
		if err != nil {
			writeLLMError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, LLMLoginResponse{
			ProviderID:     providerID,
			ProjectID:      projectID,
			ProfileID:      profileID,
			LoginSessionID: "lmstudio-api-key-" + profileID,
			Mode:           mode,
			Status:         "authenticated",
			Authenticated:  true,
			BaseURL:        configured.BaseURL,
			AccountID:      configured.AccountID,
			ModelCount:     configured.ModelCount,
			SavedAt:        configured.SavedAt,
		})
	}
}

func (h *Handler) handleLLMLoginStatus(w http.ResponseWriter, r *http.Request, projectID, sessionID string) {
	if sessionID == "" || len(sessionID) > 120 {
		writeLLMError(w, security.NewError("ERR_LLM_REQUEST_INVALID", "loginSessionId invalido", http.StatusBadRequest))
		return
	}
	providerID, profileID, err := normalizeLLMIdentity(r.URL.Query().Get("providerId"), r.URL.Query().Get("profileId"))
	if err != nil {
		writeLLMError(w, err)
		return
	}
	if providerID == providerLMStudio {
		status, err := h.lmStudioStatus(r.Context(), projectID, profileID)
		if err != nil {
			writeLLMError(w, err)
			return
		}
		state := "pending"
		if status.Authenticated {
			state = "authenticated"
		}
		writeJSON(w, http.StatusOK, LLMLoginResponse{
			ProviderID:     providerID,
			ProjectID:      projectID,
			ProfileID:      profileID,
			LoginSessionID: sessionID,
			Mode:           "api_key",
			Status:         state,
			Authenticated:  status.Authenticated,
			BaseURL:        status.BaseURL,
			AccountID:      status.AccountID,
		})
		return
	}
	session, err := h.loginSessionResponse(projectID, sessionID)
	if err != nil {
		writeLLMError(w, err)
		return
	}
	state := session.Status
	authenticated := state == "completed"
	if authenticated {
		state = "authenticated"
	}
	writeJSON(w, http.StatusOK, LLMLoginResponse{
		ProviderID:     providerID,
		ProjectID:      projectID,
		ProfileID:      session.ProfileID,
		LoginSessionID: session.LoginSessionID,
		Mode:           session.Mode,
		Status:         state,
		Authenticated:  authenticated,
		ExpiresAt:      session.ExpiresAt,
		CompletedAt:    session.CompletedAt,
		Error:          publicLLMError(session.Error),
	})
}

func stringLLMAuthField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func (h *Handler) handleLLMStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	providerID, profileID, err := normalizeLLMIdentity(r.URL.Query().Get("providerId"), r.URL.Query().Get("profileId"))
	if err != nil {
		writeLLMError(w, err)
		return
	}
	response, err := h.llmStatus(r.Context(), providerID, projectID, profileID)
	if err != nil {
		writeLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) llmStatus(ctx context.Context, providerID, projectID, profileID string) (*LLMStatusResponse, error) {
	switch providerID {
	case providerOpenAI:
		status, err := h.openAIStatus(ctx, projectID, profileID)
		if err != nil {
			return nil, err
		}
		return &LLMStatusResponse{
			Authenticated: status.Authenticated,
			ProviderID:    providerID,
			ProjectID:     projectID,
			ProfileID:     profileID,
			AccountID:     status.AccountID,
			Email:         status.Email,
			PlanType:      status.ChatGPTPlanType,
			Expires:       status.Expires,
		}, nil
	case providerLMStudio:
		status, err := h.lmStudioStatus(ctx, projectID, profileID)
		if err != nil {
			return nil, err
		}
		return &LLMStatusResponse{
			Authenticated: status.Authenticated,
			ProviderID:    providerID,
			ProjectID:     projectID,
			ProfileID:     profileID,
			AccountID:     status.AccountID,
			BaseURL:       status.BaseURL,
		}, nil
	default:
		return nil, unknownProviderError(providerID)
	}
}

func (h *Handler) handleLLMRefresh(w http.ResponseWriter, r *http.Request, projectID string) {
	var input llmRefreshRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &input); err != nil {
		writeLLMError(w, err)
		return
	}
	providerID, profileID, err := normalizeLLMIdentity(input.ProviderID, input.ProfileID)
	if err != nil {
		writeLLMError(w, err)
		return
	}
	if providerID != providerOpenAI {
		writeLLMError(w, security.NewError("ERR_LLM_REFRESH_UNSUPPORTED", "provider nao suporta refresh", http.StatusBadRequest))
		return
	}
	status, err := h.refreshOpenAI(r.Context(), projectID, profileID)
	if err != nil {
		writeLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, LLMStatusResponse{
		Authenticated: status.Authenticated,
		ProviderID:    providerID,
		ProjectID:     projectID,
		ProfileID:     profileID,
		AccountID:     status.AccountID,
		Email:         status.Email,
		PlanType:      status.ChatGPTPlanType,
		Expires:       status.Expires,
	})
}

func (h *Handler) handleLLMResponses(w http.ResponseWriter, r *http.Request, projectID string) {
	providerID, profileID, request, err := readLLMResponseRequest(w, r, h.cfg.HTTP.MaxBodyBytes)
	if err != nil {
		writeLLMError(w, err)
		return
	}
	var response *codex.CodexResponseStream
	switch providerID {
	case providerOpenAI:
		response, err = h.sendCodexResponse(r.Context(), projectID, profileID, request)
	case providerLMStudio:
		response, err = h.sendLMStudioResponse(r.Context(), projectID, profileID, request)
	default:
		err = unknownProviderError(providerID)
	}
	if err != nil {
		writeLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func readLLMResponseRequest(w http.ResponseWriter, r *http.Request, maxBytes int64) (string, string, codex.CodexResponseRequest, error) {
	var raw map[string]json.RawMessage
	if err := readJSON(w, r, maxBytes, &raw); err != nil {
		return "", "", codex.CodexResponseRequest{}, err
	}
	var providerID, profileID string
	if value := raw["providerId"]; len(value) > 0 {
		_ = json.Unmarshal(value, &providerID)
	}
	if value := raw["profileId"]; len(value) > 0 {
		_ = json.Unmarshal(value, &profileID)
	}
	providerID, profileID, err := normalizeLLMIdentity(providerID, profileID)
	if err != nil {
		return "", "", codex.CodexResponseRequest{}, err
	}
	delete(raw, "providerId")
	delete(raw, "profileId")
	payload, err := json.Marshal(raw)
	if err != nil {
		return "", "", codex.CodexResponseRequest{}, security.NewError("ERR_LLM_REQUEST_INVALID", "payload LLM invalido", http.StatusBadRequest)
	}
	var request codex.CodexResponseRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return "", "", codex.CodexResponseRequest{}, security.NewError("ERR_LLM_REQUEST_INVALID", "payload LLM invalido", http.StatusBadRequest)
	}
	return providerID, profileID, request, nil
}

func normalizeLLMIdentity(providerID, profileID string) (string, string, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	profileID = security.NormalizeProfileID(strings.TrimSpace(profileID))
	if providerID != providerOpenAI && providerID != providerLMStudio {
		return "", "", unknownProviderError(providerID)
	}
	if !security.ValidProfileID(profileID) {
		return "", "", security.NewError("ERR_LLM_REQUEST_INVALID", "profileId invalido", http.StatusBadRequest)
	}
	return providerID, profileID, nil
}

func unknownProviderError(providerID string) error {
	if providerID == "" {
		return security.NewError("ERR_LLM_REQUEST_INVALID", "providerId obrigatorio", http.StatusBadRequest)
	}
	return security.NewError("ERR_LLM_PROVIDER_UNKNOWN", "provider LLM desconhecido", http.StatusBadRequest)
}

func writeLLMError(w http.ResponseWriter, err error) {
	writeError(w, normalizeLLMError(err))
}

func publicLLMError(err *security.AppError) *security.AppError {
	if err == nil {
		return nil
	}
	return security.Public(normalizeLLMError(err))
}

func normalizeLLMError(err error) error {
	switch security.Code(err) {
	case "ERR_LLM_PROVIDER_UNKNOWN", "ERR_LLM_REQUEST_INVALID", "ERR_LLM_REFRESH_UNSUPPORTED":
		return err
	case "ERR_AUTH_PROFILE_NOT_FOUND", "ERR_CODEX_ACCOUNT_ID_MISSING":
		return security.NewError("ERR_LLM_AUTH_REQUIRED", "autenticacao do provider necessaria", http.StatusUnauthorized)
	case "ERR_CODEX_AUTH_REJECTED", "ERR_LMSTUDIO_AUTH_REJECTED":
		return security.NewError("ERR_LLM_AUTH_EXPIRED", "credencial do provider expirada ou invalida", http.StatusUnauthorized)
	case "ERR_LOGIN_SESSION_EXPIRED":
		return security.NewError("ERR_LLM_AUTH_EXPIRED", "sessao de autenticacao expirada", http.StatusUnauthorized)
	case "ERR_LOGIN_SESSION_NOT_FOUND":
		return security.NewError("ERR_LLM_REQUEST_INVALID", "sessao de autenticacao nao encontrada", http.StatusNotFound)
	case "ERR_CODEX_RATE_LIMITED", "ERR_LMSTUDIO_RATE_LIMITED":
		return security.NewError("ERR_LLM_RATE_LIMITED", "provider aplicou rate limit", http.StatusTooManyRequests)
	case "ERR_CODEX_TIMEOUT", "ERR_LMSTUDIO_TIMEOUT":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "timeout ao chamar provider LLM", http.StatusGatewayTimeout)
	case "ERR_CONTEXT_CANCELLED":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "request LLM cancelado", http.StatusRequestTimeout)
	case "ERR_DEVICE_CODE_REQUEST_FAILED":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "OpenAI recusou o inicio do login por device-code", http.StatusBadGateway)
	case "ERR_DEVICE_CODE_EXCHANGE_FAILED":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "OpenAI recusou a confirmacao do login por device-code", http.StatusBadGateway)
	case "ERR_DEVICE_CODE_RESPONSE_INVALID":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "OpenAI retornou resposta invalida no login por device-code", http.StatusBadGateway)
	case "ERR_TOKEN_EXCHANGE_FAILED", "ERR_TOKEN_RESPONSE_INVALID":
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "OpenAI recusou a troca final do login por token", http.StatusBadGateway)
	case "ERR_INVALID_JSON", "ERR_PAYLOAD_TOO_LARGE", "ERR_INVALID_PROFILE_ID", "ERR_INVALID_PROVIDER_ID",
		"ERR_CODEX_REQUEST_INVALID", "ERR_LMSTUDIO_REQUEST_INVALID", "ERR_LMSTUDIO_BASE_URL_INVALID", "ERR_LMSTUDIO_API_KEY_REQUIRED":
		return security.NewError("ERR_LLM_REQUEST_INVALID", security.Public(err).Message, security.StatusCode(err))
	default:
		return security.NewError("ERR_LLM_PROVIDER_UNAVAILABLE", "provider LLM indisponivel", http.StatusBadGateway)
	}
}
