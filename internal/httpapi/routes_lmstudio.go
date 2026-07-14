package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/lmstudio"
	"github.com/irinery/middlewareAuth/internal/security"
)

type lmStudioAPIKeyRequest struct {
	ProfileID string `json:"profileId"`
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey"`
}

type LMStudioAPIKeyResponse struct {
	Authenticated bool   `json:"authenticated"`
	ProviderID    string `json:"providerId"`
	ProjectID     string `json:"projectId"`
	ProfileID     string `json:"profileId"`
	BaseURL       string `json:"baseUrl"`
	AccountID     string `json:"accountId"`
	ModelCount    int    `json:"modelCount"`
	SavedAt       int64  `json:"savedAt"`
}

type LMStudioStatusResponse struct {
	Authenticated bool   `json:"authenticated"`
	ProviderID    string `json:"providerId"`
	ProjectID     string `json:"projectId"`
	ProfileID     string `json:"profileId"`
	BaseURL       string `json:"baseUrl,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
}

func (h *Handler) handleLMStudioAPIKey(w http.ResponseWriter, r *http.Request, projectID string) {
	var input lmStudioAPIKeyRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &input); err != nil {
		writeError(w, err)
		return
	}
	response, err := h.configureLMStudio(r.Context(), projectID, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) configureLMStudio(ctx context.Context, projectID string, input lmStudioAPIKeyRequest) (*LMStudioAPIKeyResponse, error) {
	profileID := security.NormalizeProfileID(input.ProfileID)
	if !security.ValidProfileID(profileID) {
		return nil, security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest)
	}
	baseURL := lmstudio.NormalizeBaseURL(input.BaseURL)
	if err := lmstudio.ValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if apiKey == "" || len(apiKey) > 4096 {
		return nil, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest)
	}
	models, err := lmstudio.NewTransport(nil).ListModels(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	accountID := lmstudio.AccountID(baseURL)
	save, err := h.store.SaveProviderCredential(ctx, "lmstudio", "api_key", projectID, profileID, auth.StoredOAuthCredential{
		Access:    apiKey,
		AccountID: accountID,
		BaseURL:   baseURL,
		Expires:   time.Now().Add(3650 * 24 * time.Hour).UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	return &LMStudioAPIKeyResponse{
		Authenticated: true,
		ProviderID:    "lmstudio",
		ProjectID:     projectID,
		ProfileID:     profileID,
		BaseURL:       baseURL,
		AccountID:     accountID,
		ModelCount:    len(models),
		SavedAt:       save.SavedAt,
	}, nil
}

func (h *Handler) handleLMStudioStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	response, err := h.lmStudioStatus(r.Context(), projectID, profileID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) lmStudioStatus(ctx context.Context, projectID, profileID string) (*LMStudioStatusResponse, error) {
	credential, err := h.store.LoadProviderCredential(ctx, "lmstudio", projectID, profileID)
	if err != nil {
		if security.Code(err) == "ERR_AUTH_PROFILE_NOT_FOUND" {
			return &LMStudioStatusResponse{Authenticated: false, ProviderID: "lmstudio", ProjectID: projectID, ProfileID: profileID}, nil
		}
		return nil, err
	}
	return &LMStudioStatusResponse{
		Authenticated: true,
		ProviderID:    "lmstudio",
		ProjectID:     projectID,
		ProfileID:     profileID,
		BaseURL:       credential.BaseURL,
		AccountID:     credential.AccountID,
	}, nil
}

func (h *Handler) handleLMStudioResponses(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	var request codex.CodexResponseRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &request); err != nil {
		writeError(w, err)
		return
	}
	response, err := h.sendLMStudioResponse(r.Context(), projectID, profileID, request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) sendLMStudioResponse(ctx context.Context, projectID, profileID string, request codex.CodexResponseRequest) (*codex.CodexResponseStream, error) {
	credential, err := h.store.LoadProviderCredential(ctx, "lmstudio", projectID, profileID)
	if err != nil {
		return nil, err
	}
	return lmstudio.NewTransport(nil).SendResponse(ctx, credential.BaseURL, credential.Access, request)
}
