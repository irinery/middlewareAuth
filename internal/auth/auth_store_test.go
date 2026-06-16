package auth

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

func testStore(t *testing.T) *FileStore {
	t.Helper()
	cfg, err := config.LoadConfig(context.Background(), map[string]string{
		"MIDDLEWARE_STATE_DIR":    t.TempDir(),
		"MIDDLEWARE_SECRET_KEY":   "test-secret-key-with-32-characters!!",
		"MIDDLEWARE_CLIENT_TOKEN": "test-middleware-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewFileStore(*cfg)
}

func TestFileStoreSaveEncryptsAndLoadsProfile(t *testing.T) {
	store := testStore(t)
	credential := StoredOAuthCredential{
		Access:    "access-token-secret",
		Refresh:   "refresh-token-secret",
		Expires:   time.Now().Add(time.Hour).UnixMilli(),
		AccountID: "account-1",
		Email:     "dev@example.com",
	}
	if _, err := store.SaveAuthProfile(context.Background(), "projectA", "default", credential); err != nil {
		t.Fatalf("SaveAuthProfile() error = %v", err)
	}
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), credential.Access) || strings.Contains(string(raw), credential.Refresh) {
		t.Fatalf("store leaked tokens: %s", raw)
	}
	loaded, err := store.LoadAuthProfile(context.Background(), "projectA", "default")
	if err != nil {
		t.Fatalf("LoadAuthProfile() error = %v", err)
	}
	if loaded.Access != credential.Access || loaded.Refresh != credential.Refresh || loaded.AccountID != credential.AccountID {
		t.Fatalf("loaded credential mismatch: %#v", loaded)
	}
}

func TestFileStoreSavesProviderAPIKeyWithoutPlaintext(t *testing.T) {
	store := testStore(t)
	apiKey := "local-api-key"
	if _, err := store.SaveProviderCredential(context.Background(), "lmstudio", "api_key", "projectA", "default", StoredOAuthCredential{
		Access:    apiKey,
		AccountID: "lmstudio:127.0.0.1:1234",
		BaseURL:   "http://127.0.0.1:1234",
	}); err != nil {
		t.Fatalf("SaveProviderCredential() error = %v", err)
	}
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), apiKey) {
		t.Fatalf("store leaked api key: %s", raw)
	}
	loaded, err := store.LoadProviderCredential(context.Background(), "lmstudio", "projectA", "default")
	if err != nil {
		t.Fatalf("LoadProviderCredential() error = %v", err)
	}
	if loaded.Access != apiKey || loaded.BaseURL != "http://127.0.0.1:1234" || loaded.Provider != "lmstudio" {
		t.Fatalf("loaded credential mismatch: %#v", loaded)
	}
	if _, err := store.LoadAuthProfile(context.Background(), "projectA", "default"); security.Code(err) != "ERR_AUTH_PROFILE_NOT_FOUND" {
		t.Fatalf("openai profile should not match lmstudio credential: %v", err)
	}
}

func TestFileStoreCorruptedStoreFailsClosed(t *testing.T) {
	store := testStore(t)
	if err := os.WriteFile(store.path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadAuthProfile(context.Background(), "projectA", "default")
	if security.Code(err) != "ERR_AUTH_STORE_CORRUPTED" {
		t.Fatalf("error code = %s, want ERR_AUTH_STORE_CORRUPTED (%v)", security.Code(err), err)
	}
	raw, _ := os.ReadFile(store.path)
	if string(raw) != "{broken" {
		t.Fatalf("corrupted store was modified")
	}
}

func TestFileStoreSerializesConcurrentSameProfileWrites(t *testing.T) {
	store := testStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, token := range []string{"access-a", "access-b"} {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			_, err := store.SaveAuthProfile(context.Background(), "projectA", "default", StoredOAuthCredential{
				Access:    token,
				Refresh:   "refresh-" + token,
				Expires:   time.Now().Add(time.Hour).UnixMilli(),
				AccountID: "account-1",
			})
			errs <- err
		}(token)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save error = %v", err)
		}
	}
	loaded, err := store.LoadAuthProfile(context.Background(), "projectA", "default")
	if err != nil {
		t.Fatalf("LoadAuthProfile() error = %v", err)
	}
	if loaded.Access != "access-a" && loaded.Access != "access-b" {
		t.Fatalf("unexpected access token after concurrent writes: %q", loaded.Access)
	}
}
