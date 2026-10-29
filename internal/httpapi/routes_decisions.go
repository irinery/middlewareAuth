package httpapi

import (
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/decision"
	"github.com/irinery/middlewareAuth/internal/security"
)

type decisionProvider struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Auth         LLMProviderAuth `json:"auth"`
	DefaultModel string          `json:"defaultModel"`
}

type decisionProvidersResponse struct {
	ContractVersion string             `json:"contractVersion"`
	Providers       []decisionProvider `json:"providers"`
}

type decisionLoginRequest struct {
	ProviderID string `json:"providerId"`
	ProfileID  string `json:"profileId"`
	APIKey     string `json:"apiKey"`
}

type decisionLoginResponse struct {
	ProviderID    string `json:"providerId"`
	ProjectID     string `json:"projectId"`
	ProfileID     string `json:"profileId"`
	Authenticated bool   `json:"authenticated"`
	SavedAt       int64  `json:"savedAt,omitempty"`
}

type decisionEvaluateRequest struct {
	ProviderID string `json:"providerId"`
	ProfileID  string `json:"profileId"`
	decision.Request
}

type decisionStatusResponse struct {
	Authenticated bool   `json:"authenticated"`
	ProviderID    string `json:"providerId"`
	ProjectID     string `json:"projectId"`
	ProfileID     string `json:"profileId"`
	AccountID     string `json:"accountId,omitempty"`
}

func (h *Handler) handleDecisionProviders(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profileId"))
	if profileID == "" {
		profileID = "default"
	}
	if !security.ValidProfileID(security.NormalizeProfileID(profileID)) {
		writeError(w, security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest))
		return
	}
	writeJSON(w, http.StatusOK, decisionProvidersResponse{
		ContractVersion: "middlewareauth.decisions.v1",
		Providers: []decisionProvider{
			{ID: decision.TypeSafe, Title: "TypeSafe Jev", Auth: decisionAPIKeyAuth("TypeSafe API key"), DefaultModel: "jev-latest"},
			{ID: decision.OpenRouter, Title: "OpenRouter Jev", Auth: decisionAPIKeyAuth("OpenRouter API key"), DefaultModel: "~typesafe/jev-latest"},
		},
	})
}

func decisionAPIKeyAuth(title string) LLMProviderAuth {
	return LLMProviderAuth{Required: true, Modes: []string{"api_key"}, DefaultMode: "api_key", Fields: []LLMProviderAuthField{{ID: "apiKey", Title: title, Type: "password", Required: true, Secret: true}}}
}

func (h *Handler) handleDecisionLogin(w http.ResponseWriter, r *http.Request, projectID string) {
	var input decisionLoginRequest
	if err := readJSON(w, r, 16<<10, &input); err != nil {
		writeError(w, err)
		return
	}
	providerID, profileID, err := normalizeDecisionIdentity(input.ProviderID, input.ProfileID)
	if err != nil {
		writeError(w, err)
		return
	}
	transport := decision.NewTransport(h.client)
	if err := transport.VerifyKey(r.Context(), providerID, input.APIKey); err != nil {
		writeError(w, normalizeDecisionError(err))
		return
	}
	saved, err := h.store.SaveProviderCredential(r.Context(), providerID, "api_key", projectID, profileID, auth.StoredOAuthCredential{Access: input.APIKey, AccountID: providerID + ":api-key"})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionLoginResponse{ProviderID: providerID, ProjectID: projectID, ProfileID: profileID, Authenticated: true, SavedAt: saved.SavedAt})
}

func (h *Handler) handleDecisionStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	providerID, profileID, err := normalizeDecisionIdentity(r.URL.Query().Get("providerId"), r.URL.Query().Get("profileId"))
	if err != nil {
		writeError(w, err)
		return
	}
	credential, err := h.store.LoadProviderCredential(r.Context(), providerID, projectID, profileID)
	if security.Code(err) == "ERR_AUTH_PROFILE_NOT_FOUND" {
		writeJSON(w, http.StatusOK, decisionStatusResponse{ProviderID: providerID, ProjectID: projectID, ProfileID: profileID})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionStatusResponse{Authenticated: true, ProviderID: providerID, ProjectID: projectID, ProfileID: profileID, AccountID: credential.AccountID})
}

func (h *Handler) handleDecisionEvaluate(w http.ResponseWriter, r *http.Request, projectID string) {
	var input decisionEvaluateRequest
	if err := readJSON(w, r, 1<<20, &input); err != nil {
		writeError(w, err)
		return
	}
	providerID, profileID, err := normalizeDecisionIdentity(input.ProviderID, input.ProfileID)
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(input.Model) == "" {
		if providerID == decision.TypeSafe {
			input.Model = "jev-latest"
		} else {
			input.Model = "~typesafe/jev-latest"
		}
	}
	credential, err := h.store.LoadProviderCredential(r.Context(), providerID, projectID, profileID)
	if err != nil {
		writeError(w, normalizeDecisionError(err))
		return
	}
	response, err := decision.NewTransport(h.client).Evaluate(r.Context(), providerID, credential.Access, input.Request)
	if err != nil {
		writeError(w, normalizeDecisionError(err))
		return
	}
	response.ProviderID, response.ProjectID, response.ProfileID = providerID, projectID, profileID
	writeJSON(w, http.StatusOK, response)
}

func normalizeDecisionIdentity(providerID, profileID string) (string, string, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	profileID = security.NormalizeProfileID(strings.TrimSpace(profileID))
	if providerID != decision.TypeSafe && providerID != decision.OpenRouter {
		return "", "", security.NewError("ERR_DECISION_PROVIDER_UNKNOWN", "provider de decisao desconhecido", http.StatusBadRequest)
	}
	if !security.ValidProfileID(profileID) {
		return "", "", security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest)
	}
	return providerID, profileID, nil
}

func normalizeDecisionError(err error) error {
	switch security.Code(err) {
	case "ERR_AUTH_PROFILE_NOT_FOUND":
		return security.NewError("ERR_DECISION_AUTH_REQUIRED", "autenticacao do provider necessaria", http.StatusUnauthorized)
	case "ERR_DECISION_AUTH_REJECTED":
		return security.NewError("ERR_DECISION_AUTH_EXPIRED", "credencial do provider expirada ou invalida", http.StatusUnauthorized)
	case "ERR_DECISION_RATE_LIMITED":
		return security.NewError("ERR_DECISION_RATE_LIMITED", "provider aplicou rate limit", http.StatusTooManyRequests)
	case "ERR_DECISION_TIMEOUT":
		return security.NewError("ERR_DECISION_PROVIDER_UNAVAILABLE", "timeout ou sobrecarga do provider", http.StatusGatewayTimeout)
	case "ERR_DECISION_REQUEST_INVALID", "ERR_INVALID_JSON", "ERR_PAYLOAD_TOO_LARGE":
		public := security.Public(err)
		return security.NewError("ERR_DECISION_REQUEST_INVALID", public.Message, public.StatusCode)
	default:
		return err
	}
}
