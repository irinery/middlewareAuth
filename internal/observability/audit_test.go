package observability

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestAuditEventDoesNotLeakTokens(t *testing.T) {
	cfg, err := config.LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	auditor := NewAuditor(*cfg)
	if _, err := auditor.AuditEvent(context.Background(), AuditEvent{
		Type:      "LOGIN_COMPLETED",
		ProjectID: "acme",
		ProfileID: "default",
		Provider:  "openai",
	}); err != nil {
		t.Fatalf("AuditEvent() error = %v", err)
	}
	raw, err := os.ReadFile(auditor.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "access_token") || strings.Contains(string(raw), "refresh_token") {
		t.Fatalf("audit leaked token fields: %s", raw)
	}
}

func TestAuditRejectsSensitiveMetadata(t *testing.T) {
	cfg, err := config.LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	auditor := NewAuditor(*cfg)
	_, err = auditor.AuditEvent(context.Background(), AuditEvent{
		Type:      "LOGIN_COMPLETED",
		ProjectID: "acme",
		Provider:  "openai",
		Metadata:  []AuditMetadataPair{{Key: "refresh_token", Value: "secret"}},
	})
	if security.Code(err) != "ERR_AUDIT_EVENT_INVALID" {
		t.Fatalf("code = %s, want ERR_AUDIT_EVENT_INVALID (%v)", security.Code(err), err)
	}
}
