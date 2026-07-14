package httpapi

import (
	"context"
	"net/http"

	"github.com/irinery/middlewareAuth/internal/codex"
)

func (h *Handler) handleCodexResponses(w http.ResponseWriter, r *http.Request, projectID string) {
	profileID := profileFromRequest(r)
	var request codex.CodexResponseRequest
	if err := readJSON(w, r, h.cfg.HTTP.MaxBodyBytes, &request); err != nil {
		writeError(w, err)
		return
	}
	response, err := h.sendCodexResponse(r.Context(), projectID, profileID, request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) sendCodexResponse(ctx context.Context, projectID, profileID string, request codex.CodexResponseRequest) (*codex.CodexResponseStream, error) {
	credential, err := h.refresher.ResolveFreshCredential(ctx, projectID, profileID, 60000)
	if err != nil {
		return nil, err
	}
	return h.codex.SendCodexResponse(ctx, *credential, request, codex.CodexTransportOptions{
		TimeoutMs:  h.cfg.Codex.RequestTimeoutMs,
		MaxRetries: h.cfg.Codex.MaxRetries,
	})
}
