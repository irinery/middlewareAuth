package httpapi

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/oauth"
	"github.com/irinery/middlewareAuth/internal/security"
)

type CredentialRefresher interface {
	ResolveFreshCredential(ctx context.Context, projectID string, profileID string, minTTLms int64) (*auth.StoredOAuthCredential, error)
}

type CodexSender interface {
	SendCodexResponse(ctx context.Context, credential auth.StoredOAuthCredential, request codex.CodexResponseRequest, options codex.CodexTransportOptions) (*codex.CodexResponseStream, error)
}

type CodexModelLister interface {
	ListCodexModels(ctx context.Context, credential auth.StoredOAuthCredential) ([]codex.CodexModelInfo, error)
}

type Handler struct {
	cfg       config.Config
	store     auth.ProfileStore
	refresher CredentialRefresher
	codex     CodexSender
	client    *http.Client
	logger    *slog.Logger

	sessionMu  sync.Mutex
	sessions   map[string]*loginSession
	stateIndex map[string]string
}

type loginSession struct {
	LoginSessionID string
	ProjectID      string
	ProfileID      string
	Mode           string
	Status         string
	Flow           oauth.AuthorizationFlow
	ExpiresAt      int64
	CompletedAt    int64
	Error          *security.AppError
}

type LoginSessionResponse struct {
	LoginSessionID string             `json:"loginSessionId"`
	ProjectID      string             `json:"projectId"`
	ProfileID      string             `json:"profileId"`
	Mode           string             `json:"mode"`
	Status         string             `json:"status"`
	ExpiresAt      int64              `json:"expiresAt"`
	CompletedAt    int64              `json:"completedAt,omitempty"`
	Error          *security.AppError `json:"error,omitempty"`
}

func NewHandler(cfg config.Config, store auth.ProfileStore, refresher CredentialRefresher, codexSender CodexSender, client *http.Client) *Handler {
	if client == nil {
		client = config.NewHTTPClient(cfg.Codex)
	}
	return &Handler{
		cfg:        cfg,
		store:      store,
		refresher:  refresher,
		codex:      codexSender,
		client:     client,
		logger:     slog.Default(),
		sessions:   make(map[string]*loginSession),
		stateIndex: make(map[string]string),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		h.logger.InfoContext(r.Context(), "http_request",
			slog.String("method", r.Method),
			slog.String("path", security.Redact(r.URL.Path)),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("remote", r.RemoteAddr),
		)
	}()
	w = rec
	if r.URL.Path == "/healthz" {
		h.handleHealth(w, r)
		return
	}
	if r.URL.Path == "/v1/auth/openai/callback" {
		h.handleCallback(w, r)
		return
	}
	parts := splitPath(r.URL.Path)
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "projects" {
		projectID := parts[2]
		if !security.ValidProjectID(projectID) {
			writeError(w, security.NewError("ERR_INVALID_PROJECT_ID", "projectId invalido", http.StatusBadRequest))
			return
		}
		if !h.authorized(r) {
			writeError(w, security.NewError("ERR_MIDDLEWARE_UNAUTHORIZED", "credencial interna invalida", http.StatusUnauthorized))
			return
		}
		h.handleProjectRoute(w, r, projectID, parts[3:])
		return
	}
	writeError(w, security.NewError("ERR_NOT_FOUND", "rota nao encontrada", http.StatusNotFound))
}

func (h *Handler) handleProjectRoute(w http.ResponseWriter, r *http.Request, projectID string, rest []string) {
	if len(rest) == 2 && rest[0] == "llm" && rest[1] == "providers" && r.Method == http.MethodGet {
		h.handleLLMProviders(w, r, projectID)
		return
	}
	if len(rest) == 2 && rest[0] == "llm" && rest[1] == "login" && r.Method == http.MethodPost {
		h.handleLLMLogin(w, r, projectID)
		return
	}
	if len(rest) == 3 && rest[0] == "llm" && rest[1] == "login-sessions" && r.Method == http.MethodGet {
		h.handleLLMLoginStatus(w, r, projectID, rest[2])
		return
	}
	if len(rest) == 2 && rest[0] == "llm" && rest[1] == "status" && r.Method == http.MethodGet {
		h.handleLLMStatus(w, r, projectID)
		return
	}
	if len(rest) == 2 && rest[0] == "llm" && rest[1] == "refresh" && r.Method == http.MethodPost {
		h.handleLLMRefresh(w, r, projectID)
		return
	}
	if len(rest) == 2 && rest[0] == "llm" && rest[1] == "responses" && r.Method == http.MethodPost {
		h.handleLLMResponses(w, r, projectID)
		return
	}
	if len(rest) == 3 && rest[0] == "auth" && rest[1] == "openai" && rest[2] == "login" && r.Method == http.MethodPost {
		h.handleLogin(w, r, projectID)
		return
	}
	if len(rest) == 4 && rest[0] == "auth" && rest[1] == "openai" && rest[2] == "login-sessions" && r.Method == http.MethodGet {
		h.handleLoginSessionStatus(w, r, projectID, rest[3])
		return
	}
	if len(rest) == 3 && rest[0] == "auth" && rest[1] == "openai" && rest[2] == "status" && r.Method == http.MethodGet {
		h.handleStatus(w, r, projectID)
		return
	}
	if len(rest) == 3 && rest[0] == "auth" && rest[1] == "openai" && rest[2] == "refresh" && r.Method == http.MethodPost {
		h.handleRefresh(w, r, projectID)
		return
	}
	if len(rest) == 3 && rest[0] == "auth" && rest[1] == "lmstudio" && rest[2] == "api-key" && r.Method == http.MethodPost {
		h.handleLMStudioAPIKey(w, r, projectID)
		return
	}
	if len(rest) == 3 && rest[0] == "auth" && rest[1] == "lmstudio" && rest[2] == "status" && r.Method == http.MethodGet {
		h.handleLMStudioStatus(w, r, projectID)
		return
	}
	if len(rest) == 2 && rest[0] == "codex" && rest[1] == "responses" && r.Method == http.MethodPost {
		h.handleCodexResponses(w, r, projectID)
		return
	}
	if len(rest) == 2 && rest[0] == "lmstudio" && rest[1] == "responses" && r.Method == http.MethodPost {
		h.handleLMStudioResponses(w, r, projectID)
		return
	}
	writeError(w, security.NewError("ERR_NOT_FOUND", "rota nao encontrada", http.StatusNotFound))
}

func (h *Handler) authorized(r *http.Request) bool {
	expected := h.cfg.Security.MiddlewareToken
	if expected == "" {
		return false
	}
	if tokensEqual(r.Header.Get("X-Middleware-Token"), expected) {
		return true
	}
	authz := r.Header.Get("Authorization")
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return false
	}
	return tokensEqual(strings.TrimSpace(strings.TrimPrefix(authz, bearer)), expected)
}

func tokensEqual(got, expected string) bool {
	if len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func (h *Handler) addSession(session loginSession) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.cleanupSessionsLocked()
	if len(h.sessions) >= 1000 {
		return security.NewError("ERR_LOGIN_START_FAILED", "limite de sessoes recentes atingido", http.StatusConflict)
	}
	copy := session
	h.sessions[session.LoginSessionID] = &copy
	if session.Flow.State != "" {
		h.stateIndex[session.Flow.State] = session.LoginSessionID
	}
	return nil
}

func (h *Handler) sessionByState(state string) (loginSession, bool) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	sessionID, ok := h.stateIndex[state]
	if !ok {
		return loginSession{}, false
	}
	session, ok := h.sessions[sessionID]
	if !ok {
		delete(h.stateIndex, state)
		return loginSession{}, false
	}
	return *session, ok
}

func (h *Handler) removeStateIndex(state string) {
	if state == "" {
		return
	}
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.stateIndex, state)
}

func (h *Handler) markSessionCompleted(sessionID string) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		session.Status = "completed"
		session.CompletedAt = time.Now().UnixMilli()
		session.Error = nil
		h.clearSessionFlowLocked(session)
	}
}

func (h *Handler) markSessionFailed(sessionID string, err error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		session.Status = "failed"
		session.CompletedAt = time.Now().UnixMilli()
		session.Error = security.Public(err)
		h.clearSessionFlowLocked(session)
	}
}

func (h *Handler) markSessionExpired(sessionID string) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		session.Status = "expired"
		session.Error = security.NewError("ERR_LOGIN_SESSION_EXPIRED", "sessao de login expirada", http.StatusGone)
		h.clearSessionFlowLocked(session)
	}
}

func (h *Handler) loginSessionResponse(projectID, sessionID string) (*LoginSessionResponse, error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	session := h.sessions[sessionID]
	if session == nil || session.ProjectID != projectID {
		return nil, security.NewError("ERR_LOGIN_SESSION_NOT_FOUND", "sessao de login nao encontrada", http.StatusNotFound)
	}
	if session.Status == "pending" && session.ExpiresAt <= time.Now().UnixMilli() {
		session.Status = "expired"
		session.Error = security.NewError("ERR_LOGIN_SESSION_EXPIRED", "sessao de login expirada", http.StatusGone)
		h.clearSessionFlowLocked(session)
	}
	return session.response(), nil
}

func (h *Handler) clearSessionFlowLocked(session *loginSession) {
	if state := session.Flow.State; state != "" {
		delete(h.stateIndex, state)
	}
	session.Flow = oauth.AuthorizationFlow{}
}

func (h *Handler) cleanupSessionsLocked() {
	now := time.Now().UnixMilli()
	for sessionID, session := range h.sessions {
		if session.ExpiresAt+15*60*1000 <= now {
			if session.Flow.State != "" {
				delete(h.stateIndex, session.Flow.State)
			}
			delete(h.sessions, sessionID)
		}
	}
}

func (s *loginSession) response() *LoginSessionResponse {
	return &LoginSessionResponse{
		LoginSessionID: s.LoginSessionID,
		ProjectID:      s.ProjectID,
		ProfileID:      s.ProfileID,
		Mode:           s.Mode,
		Status:         s.Status,
		ExpiresAt:      s.ExpiresAt,
		CompletedAt:    s.CompletedAt,
		Error:          s.Error,
	}
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
