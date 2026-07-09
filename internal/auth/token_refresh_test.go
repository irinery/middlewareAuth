package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestResolveFreshCredentialPreservesAccountIDWhenTokenHasNoIdentity(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "not-a-jwt",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	refresher, store := testRefresher(t, tokenServer)
	saveExpiredCredential(t, store, "old-account")
	credential, err := refresher.ResolveFreshCredential(context.Background(), "projectA", "default", 60000)
	if err != nil {
		t.Fatalf("ResolveFreshCredential() error = %v", err)
	}
	if credential.AccountID != "old-account" {
		t.Fatalf("AccountID = %q", credential.AccountID)
	}
}

func TestResolveFreshCredentialRejectsChangedAccountID(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  testJWT(map[string]any{"sub": "new-account"}),
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	refresher, store := testRefresher(t, tokenServer)
	saveExpiredCredential(t, store, "old-account")
	_, err := refresher.ResolveFreshCredential(context.Background(), "projectA", "default", 60000)
	if security.Code(err) != "ERR_ACCOUNT_ID_CHANGED" {
		t.Fatalf("code = %s, want ERR_ACCOUNT_ID_CHANGED (%v)", security.Code(err), err)
	}
}

func testRefresher(t *testing.T, tokenServer *httptest.Server) (*Refresher, *FileStore) {
	t.Helper()
	cfg, err := config.LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token-32-characters",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.OAuth.AuthBaseURL = tokenServer.URL
	store := NewFileStore(*cfg)
	return NewRefresher(*cfg, store, tokenServer.Client()), store
}

func saveExpiredCredential(t *testing.T, store *FileStore, accountID string) {
	t.Helper()
	_, err := store.SaveAuthProfile(context.Background(), "projectA", "default", StoredOAuthCredential{
		Access:    "old-access",
		Refresh:   "old-refresh",
		Expires:   time.Now().Add(-time.Minute).UnixMilli(),
		AccountID: accountID,
		Email:     "dev@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
