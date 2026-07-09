package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/oauth"
	"github.com/irinery/middlewareAuth/internal/security"
)

type loginStartRequest struct {
	ProfileID string `json:"profileId"`
	Mode      string `json:"mode"`
}

type LoginStartResponse struct {
	LoginSessionID  string `json:"loginSessionId"`
	AuthURL         string `json:"authUrl,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	ExpiresAt       int64  `json:"expiresAt"`
}

type CallbackResult struct {
	Status    string `json:"status"`
	ProfileID string `json:"profileId"`
}

type StatusResponse struct {
	Authenticated   bool   `json:"authenticated"`
	ProjectID       string `json:"projectId"`
	ProfileID       string `json:"profileId"`
	AccountID       string `json:"accountId,omitempty"`
	Email           string `json:"email,omitempty"`
	ChatGPTPlanType string `json:"chatgptPlanType,omitempty"`
	Expires         int64  `json:"expires,omitempty"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request, projectID string) {
	var input loginStartRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &input); err != nil {
		writeError(w, err)
		return
	}
	profileID := security.NormalizeProfileID(input.ProfileID)
	if !security.ValidProfileID(profileID) {
		writeError(w, security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest))
		return
	}
	if input.Mode == "" {
		input.Mode = "oauth"
	}
	switch input.Mode {
	case "oauth":
		h.startOAuthLogin(w, r, projectID, profileID)
	case "device_code":
		h.startDeviceCodeLogin(w, r, projectID, profileID)
	default:
		writeError(w, security.NewError("ERR_LOGIN_START_FAILED", "modo de login invalido", http.StatusBadRequest))
	}
}

func (h *Handler) startOAuthLogin(w http.ResponseWriter, r *http.Request, projectID, profileID string) {
	flow, err := oauth.CreateAuthorizationFlow(r.Context(), h.cfg.OAuth, "middleware-codex-oauth")
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID := randomID()
	expiresAt := time.Now().Add(15 * time.Minute).UnixMilli()
	if err := h.addSession(loginSession{
		LoginSessionID: sessionID,
		ProjectID:      projectID,
		ProfileID:      profileID,
		Mode:           "oauth",
		Status:         "pending",
		Flow:           *flow,
		ExpiresAt:      expiresAt,
	}); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, LoginStartResponse{
		LoginSessionID: sessionID,
		AuthURL:        flow.URL,
		ExpiresAt:      expiresAt,
	})
}

func (h *Handler) startDeviceCodeLogin(w http.ResponseWriter, r *http.Request, projectID, profileID string) {
	device, err := oauth.RequestDeviceCode(r.Context(), h.client, h.cfg.OAuth)
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID := randomID()
	expiresAt := time.Now().Add(time.Duration(device.ExpiresInMs) * time.Millisecond).UnixMilli()
	if err := h.addSession(loginSession{
		LoginSessionID: sessionID,
		ProjectID:      projectID,
		ProfileID:      profileID,
		Mode:           "device_code",
		Status:         "pending",
		ExpiresAt:      expiresAt,
	}); err != nil {
		writeError(w, err)
		return
	}
	go func() {
		ctx, cancel := contextWithTimeout(time.Duration(device.ExpiresInMs) * time.Millisecond)
		defer cancel()
		credentials, err := oauth.PollDeviceCode(ctx, h.client, h.cfg.OAuth, *device)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded || security.Code(err) == "ERR_DEVICE_CODE_TIMEOUT" {
				h.markSessionExpired(sessionID)
				return
			}
			h.markSessionFailed(sessionID, err)
			return
		}
		if err := h.saveOAuthCredentials(ctx, projectID, profileID, credentials); err != nil {
			h.markSessionFailed(sessionID, err)
			return
		}
		h.markSessionCompleted(sessionID)
	}()
	writeJSON(w, http.StatusOK, LoginStartResponse{
		LoginSessionID:  sessionID,
		VerificationURL: device.VerificationURL,
		UserCode:        device.UserCode,
		ExpiresAt:       expiresAt,
	})
}

func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" || len(state) > 128 {
		writeError(w, security.NewError("ERR_OAUTH_STATE_MISMATCH", "state OAuth invalido", http.StatusBadRequest))
		return
	}
	session, ok := h.sessionByState(state)
	if !ok {
		writeError(w, security.NewError("ERR_OAUTH_STATE_MISMATCH", "state OAuth nao encontrado", http.StatusBadRequest))
		return
	}
	if session.ExpiresAt <= time.Now().UnixMilli() {
		h.markSessionExpired(session.LoginSessionID)
		h.removeStateIndex(state)
		writeError(w, security.NewError("ERR_LOGIN_SESSION_EXPIRED", "sessao de login expirada", http.StatusGone))
		return
	}
	if r.URL.Query().Get("error") != "" {
		err := security.NewError("ERR_OAUTH_DENIED", "autorizacao OAuth recusada", http.StatusBadRequest)
		h.markSessionFailed(session.LoginSessionID, err)
		h.removeStateIndex(state)
		writeError(w, err)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		err := security.NewError("ERR_OAUTH_MISSING_CODE", "authorization code ausente", http.StatusBadRequest)
		h.markSessionFailed(session.LoginSessionID, err)
		h.removeStateIndex(state)
		writeError(w, err)
		return
	}
	credentials, err := oauth.ExchangeAuthorizationCode(r.Context(), h.client, h.cfg.OAuth, code, session.Flow.Verifier, session.Flow.RedirectURI)
	if err != nil {
		h.markSessionFailed(session.LoginSessionID, err)
		h.removeStateIndex(state)
		writeError(w, err)
		return
	}
	if err := h.saveOAuthCredentials(r.Context(), session.ProjectID, session.ProfileID, credentials); err != nil {
		h.markSessionFailed(session.LoginSessionID, err)
		h.removeStateIndex(state)
		writeError(w, err)
		return
	}
	h.markSessionCompleted(session.LoginSessionID)
	h.removeStateIndex(state)
	writeJSON(w, http.StatusOK, CallbackResult{Status: "ok", ProfileID: session.ProfileID})
}

func (h *Handler) handleLoginSessionStatus(w http.ResponseWriter, r *http.Request, projectID string, sessionID string) {
	if sessionID == "" || len(sessionID) > 120 {
		writeError(w, security.NewError("ERR_LOGIN_SESSION_NOT_FOUND", "sessao de login nao encontrada", http.StatusNotFound))
		return
	}
	response, err := h.loginSessionResponse(projectID, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	credential, err := h.store.LoadAuthProfile(r.Context(), projectID, profileID)
	if err != nil {
		if security.Code(err) == "ERR_AUTH_PROFILE_NOT_FOUND" {
			writeJSON(w, http.StatusOK, StatusResponse{Authenticated: false, ProjectID: projectID, ProfileID: profileID})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, StatusResponse{
		Authenticated:   true,
		ProjectID:       projectID,
		ProfileID:       profileID,
		AccountID:       credential.AccountID,
		Email:           credential.Email,
		ChatGPTPlanType: credential.ChatGPTPlanType,
		Expires:         credential.Expires,
	})
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	credential, err := h.refresher.ResolveFreshCredential(r.Context(), projectID, profileID, 3600000)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, StatusResponse{
		Authenticated: true,
		ProjectID:     projectID,
		ProfileID:     profileID,
		AccountID:     credential.AccountID,
		Email:         credential.Email,
		Expires:       credential.Expires,
	})
}

func (h *Handler) saveOAuthCredentials(ctx context.Context, projectID, profileID string, credentials *oauth.OAuthCredentials) error {
	identity, err := auth.ResolveAuthIdentity(credentials.Access, credentials.Email)
	if err != nil && security.Code(err) != "ERR_JWT_PAYLOAD_INVALID" {
		return err
	}
	accountID := identity.AccountID
	if accountID == "" {
		accountID = credentials.AccountID
	}
	email := identity.Email
	if email == "" {
		email = credentials.Email
	}
	_, err = h.store.SaveAuthProfile(ctx, projectID, profileID, auth.StoredOAuthCredential{
		Access:          credentials.Access,
		Refresh:         credentials.Refresh,
		Expires:         credentials.Expires,
		AccountID:       accountID,
		Email:           email,
		ChatGPTPlanType: identity.ChatGPTPlanType,
	})
	return err
}

func profileFromRequest(r *http.Request) string {
	return security.NormalizeProfileID(r.URL.Query().Get("profileId"))
}

func randomID() string {
	raw := make([]byte, 18)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}
