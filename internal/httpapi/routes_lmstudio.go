package httpapi

import (
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
	profileID := security.NormalizeProfileID(input.ProfileID)
	if !security.ValidProfileID(profileID) {
		writeError(w, security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest))
		return
	}
	baseURL := lmstudio.NormalizeBaseURL(input.BaseURL)
	if err := lmstudio.ValidateBaseURL(baseURL); err != nil {
		writeError(w, err)
		return
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if apiKey == "" || len(apiKey) > 4096 {
		writeError(w, security.NewError("ERR_LMSTUDIO_API_KEY_REQUIRED", "apiKey LM Studio obrigatoria", http.StatusBadRequest))
		return
	}
	models, err := lmstudio.NewTransport(nil).ListModels(r.Context(), baseURL, apiKey)
	if err != nil {
		writeError(w, err)
		return
	}
	accountID := lmstudio.AccountID(baseURL)
	save, err := h.store.SaveProviderCredential(r.Context(), "lmstudio", "api_key", projectID, profileID, auth.StoredOAuthCredential{
		Access:    apiKey,
		AccountID: accountID,
		BaseURL:   baseURL,
		Expires:   time.Now().Add(3650 * 24 * time.Hour).UnixMilli(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, LMStudioAPIKeyResponse{
		Authenticated: true,
		ProviderID:    "lmstudio",
		ProjectID:     projectID,
		ProfileID:     profileID,
		BaseURL:       baseURL,
		AccountID:     accountID,
		ModelCount:    len(models),
		SavedAt:       save.SavedAt,
	})
}

func (h *Handler) handleLMStudioStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	credential, err := h.store.LoadProviderCredential(r.Context(), "lmstudio", projectID, profileID)
	if err != nil {
		if security.Code(err) == "ERR_AUTH_PROFILE_NOT_FOUND" {
			writeJSON(w, http.StatusOK, LMStudioStatusResponse{Authenticated: false, ProviderID: "lmstudio", ProjectID: projectID, ProfileID: profileID})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, LMStudioStatusResponse{
		Authenticated: true,
		ProviderID:    "lmstudio",
		ProjectID:     projectID,
		ProfileID:     profileID,
		BaseURL:       credential.BaseURL,
		AccountID:     credential.AccountID,
	})
}

func (h *Handler) handleLMStudioResponses(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	var request codex.CodexResponseRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &request); err != nil {
		writeError(w, err)
		return
	}
	credential, err := h.store.LoadProviderCredential(r.Context(), "lmstudio", projectID, profileID)
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := lmstudio.NewTransport(nil).SendResponse(r.Context(), credential.BaseURL, credential.Access, request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
