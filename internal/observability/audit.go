package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

type AuditEvent struct {
	EventID   string              `json:"eventId,omitempty"`
	Type      string              `json:"type"`
	ProjectID string              `json:"projectId"`
	ProfileID string              `json:"profileId,omitempty"`
	Provider  string              `json:"provider"`
	Timestamp int64               `json:"timestamp"`
	Metadata  []AuditMetadataPair `json:"metadata,omitempty"`
}

type AuditMetadataPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AuditWriteResult struct {
	EventID   string `json:"eventId"`
	WrittenAt int64  `json:"writtenAt"`
}

type Auditor struct {
	path string
}

func NewAuditor(cfg config.Config) *Auditor {
	return &Auditor{path: filepath.Join(cfg.StateDir, "audit.log")}
}

func (a *Auditor) AuditEvent(ctx context.Context, event AuditEvent) (*AuditWriteResult, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	select {
	case <-ctx.Done():
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado", http.StatusRequestTimeout, ctx.Err())
	default:
	}
	if err := validateAuditEvent(event); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	if event.EventID == "" {
		event.EventID = randomAuditID()
	}
	if event.Provider == "" {
		event.Provider = "openai"
	}
	if event.Timestamp == 0 {
		event.Timestamp = now
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, security.Wrap("ERR_AUDIT_WRITE_FAILED", "falha ao serializar evento de auditoria", http.StatusInternalServerError, err)
	}
	if security.Redact(string(raw)) != string(raw) {
		return nil, security.NewError("ERR_AUDIT_EVENT_INVALID", "evento de auditoria contem campo sensivel", http.StatusBadRequest)
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return nil, security.Wrap("ERR_AUDIT_WRITE_FAILED", "falha ao criar diretorio de auditoria", http.StatusInternalServerError, err)
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, security.Wrap("ERR_AUDIT_WRITE_FAILED", "falha ao abrir audit log", http.StatusInternalServerError, err)
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return nil, security.Wrap("ERR_AUDIT_WRITE_FAILED", "falha ao gravar audit log", http.StatusInternalServerError, err)
	}
	return &AuditWriteResult{EventID: event.EventID, WrittenAt: now}, nil
}

func validateAuditEvent(event AuditEvent) error {
	switch event.Type {
	case "LOGIN_STARTED", "LOGIN_COMPLETED", "LOGIN_FAILED", "TOKEN_REFRESHED", "CODEX_REQUEST", "CODEX_ERROR", "PROFILE_DELETED":
	default:
		return security.NewError("ERR_AUDIT_EVENT_INVALID", "tipo de auditoria invalido", http.StatusBadRequest)
	}
	if !security.ValidProjectID(event.ProjectID) {
		return security.NewError("ERR_AUDIT_EVENT_INVALID", "projectId invalido no evento de auditoria", http.StatusBadRequest)
	}
	if event.ProfileID != "" && !security.ValidProfileID(event.ProfileID) {
		return security.NewError("ERR_AUDIT_EVENT_INVALID", "profileId invalido no evento de auditoria", http.StatusBadRequest)
	}
	for _, pair := range event.Metadata {
		key := strings.ToLower(pair.Key)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "authorization") || strings.Contains(key, "access") || strings.Contains(key, "refresh") {
			return security.NewError("ERR_AUDIT_EVENT_INVALID", "metadata sensivel em evento de auditoria", http.StatusBadRequest)
		}
	}
	return nil
}

func randomAuditID() string {
	return "audit-" + time.Now().UTC().Format("20060102150405.000000000")
}
