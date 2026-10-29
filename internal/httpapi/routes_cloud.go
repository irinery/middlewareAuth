package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/cloud"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/security"
)

func (h *Handler) configureCloudKey(ctx context.Context, providerID, projectID, profileID, apiKey, workspaceID string) (int64, error) {
	if apiKey == "" || len(apiKey) > 4096 || strings.TrimSpace(apiKey) != apiKey || strings.ContainsAny(apiKey, "\r\n") {
		return 0, security.NewError("ERR_LLM_REQUEST_INVALID", "apiKey invalida", http.StatusBadRequest)
	}
	if providerID == cloud.Anthropic && strings.HasPrefix(apiKey, "sk-ant-oat") {
		return 0, security.NewError("ERR_LLM_REQUEST_INVALID", "use uma chave da Claude Console para a API", http.StatusBadRequest)
	}
	if providerID == cloud.OpenRouter && workspaceID != "" {
		return 0, security.NewError("ERR_LLM_REQUEST_INVALID", "workspaceId nao suportado", http.StatusBadRequest)
	}
	if workspaceID != "" && (!strings.HasPrefix(workspaceID, "wrkspc_") || len(workspaceID) > 100 || strings.ContainsAny(workspaceID, " \r\n\t")) {
		return 0, security.NewError("ERR_LLM_REQUEST_INVALID", "workspaceId invalido", http.StatusBadRequest)
	}
	if err := cloud.NewTransport(h.client).VerifyKey(ctx, providerID, apiKey, workspaceID); err != nil {
		return 0, err
	}
	accountID := providerID + ":api-key"
	saved, err := h.store.SaveProviderCredential(ctx, providerID, "api_key", projectID, profileID, auth.StoredOAuthCredential{Access: apiKey, AccountID: accountID, WorkspaceID: workspaceID})
	if err != nil {
		return 0, err
	}
	return saved.SavedAt, nil
}

func (h *Handler) sendCloudResponse(ctx context.Context, providerID, projectID, profileID string, request codex.CodexResponseRequest) (*codex.CodexResponseStream, error) {
	credential, err := h.store.LoadProviderCredential(ctx, providerID, projectID, profileID)
	if err != nil {
		return nil, err
	}
	return cloud.NewTransport(h.client).Send(ctx, providerID, credential.Access, credential.WorkspaceID, request)
}
