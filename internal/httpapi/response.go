package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/security"
)

type MiddlewareErrorResponse struct {
	Error *security.AppError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	public := security.Public(err)
	writeJSON(w, public.StatusCode, MiddlewareErrorResponse{Error: public})
}

func readJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			return security.NewError("ERR_PAYLOAD_TOO_LARGE", "payload excede limite", http.StatusRequestEntityTooLarge)
		}
		return security.Wrap("ERR_INVALID_JSON", "falha ao ler payload", http.StatusBadRequest, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return security.Wrap("ERR_INVALID_JSON", "JSON invalido", http.StatusBadRequest, err)
		}
		return security.Wrap("ERR_INVALID_JSON", "payload invalido", http.StatusBadRequest, err)
	}
	return nil
}
