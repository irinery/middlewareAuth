package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientTokenRotationPersistsAndRevokesOldToken(t *testing.T) {
	h := testHandler(t)
	rotate := func(handler *Handler, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/client-token/rotate", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := rotate(h, handlerTestToken)
	if first.Code != http.StatusOK {
		t.Fatalf("rotation status=%d body=%s", first.Code, first.Body.String())
	}
	var response struct {
		ClientToken string `json:"clientToken"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.ClientToken) < 32 || response.ClientToken == handlerTestToken {
		t.Fatal("rotation did not return a fresh token")
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("rotation response can be cached")
	}
	if got := rotate(h, handlerTestToken); got.Code != http.StatusUnauthorized {
		t.Fatalf("old token still rotates: %d", got.Code)
	}

	path := filepath.Join(h.cfg.StateDir, "client-token.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), response.ClientToken) || strings.Contains(string(raw), handlerTestToken) {
		t.Fatal("client token leaked to disk")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token store permissions=%#o", info.Mode().Perm())
	}

	restarted, err := NewHandler(h.cfg, h.store, fakeRefresher{}, fakeCodex{}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if got := rotate(restarted, handlerTestToken); got.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap token survived restart: %d", got.Code)
	}
	if got := rotate(restarted, response.ClientToken); got.Code != http.StatusOK {
		t.Fatalf("rotated token failed after restart: %d body=%s", got.Code, got.Body.String())
	}
}

func TestClientTokenRotationRequiresValidTokenAndPreservesCurrentTokenOnWriteFailure(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/client-token/rotate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", rec.Code)
	}

	path := filepath.Join(h.cfg.StateDir, "client-token.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/auth/client-token/rotate", nil)
	req.Header.Set("X-Middleware-Token", handlerTestToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("write failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !h.clientTokens.Valid(handlerTestToken) {
		t.Fatal("write failure revoked current token")
	}
}
