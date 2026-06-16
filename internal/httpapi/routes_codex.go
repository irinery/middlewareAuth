package httpapi

import (
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
	credential, err := h.refresher.ResolveFreshCredential(r.Context(), projectID, profileID, 60000)
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := h.codex.SendCodexResponse(r.Context(), *credential, request, codex.CodexTransportOptions{
		TimeoutMs:  h.cfg.Codex.RequestTimeoutMs,
		MaxRetries: h.cfg.Codex.MaxRetries,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
