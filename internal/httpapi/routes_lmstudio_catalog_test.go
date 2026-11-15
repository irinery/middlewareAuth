package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
)

func TestLLMProvidersListsOnlyModelsFromSelectedLMStudioProfile(t *testing.T) {
	const secret = "fixture-secret-lmstudio"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"id":"model-z"},{"id":"model-a"},{"id":"model-a"},{"id":""},{"id":%q},{"id":%q}]}`, secret, "sk-"+"sensitive-token-123456")
	}))
	defer upstream.Close()
	h := testHandler(t)
	body, _ := json.Marshal(map[string]any{"providerId": "lmstudio", "profileId": "work", "mode": "api_key", "authFields": map[string]any{"baseUrl": upstream.URL, "apiKey": secret}})
	login := httptest.NewRequest(http.MethodPost, "/v1/projects/pockettrace/llm/login", bytes.NewReader(body))
	login.Header.Set("Authorization", "Bearer "+handlerTestToken)
	loginResponse := httptest.NewRecorder()
	h.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("fixture login status=%d", loginResponse.Code)
	}
	get := func(profile string) LLMProvider {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/projects/pockettrace/llm/providers?profileId="+profile, nil)
		req.Header.Set("Authorization", "Bearer "+handlerTestToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("catalog status=%d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("catalog leaked fixture credential")
		}
		var catalog LLMProviderCatalogResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
			t.Fatal(err)
		}
		for _, provider := range catalog.Providers {
			if provider.ID == providerLMStudio {
				return provider
			}
		}
		t.Fatal("LM Studio provider missing")
		return LLMProvider{}
	}
	models := get("work").Models
	if len(models) != 2 || models[0].ID != "model-a" || models[1].ID != "model-z" {
		t.Fatalf("unexpected model IDs: %#v", models)
	}
	if models := get("other").Models; len(models) != 0 {
		t.Fatalf("models leaked across profiles: %#v", models)
	}
	upstream.Close()
	if models := get("work").Models; len(models) != 0 {
		t.Fatalf("unavailable upstream must not advertise cached models: %#v", models)
	}
}

func TestLMStudioCatalogDiscardsResponseAfterProfileChanges(t *testing.T) {
	const secret = "fixture-old-key"
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		close(started)
		<-release
		fmt.Fprint(w, `{"data":[{"id":"old-account-model"}]}`)
	}))
	defer upstream.Close()
	h := testHandler(t)
	ctx := context.Background()
	credential := auth.StoredOAuthCredential{Access: secret, BaseURL: upstream.URL, AccountID: "old-account", Expires: time.Now().Add(time.Hour).UnixMilli()}
	if _, err := h.store.SaveProviderCredential(ctx, "lmstudio", "api_key", "pockettrace", "work", credential); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/v1/projects/pockettrace/llm/providers?profileId=work", nil)
		req.Header.Set("Authorization", "Bearer "+handlerTestToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("upstream request did not start")
	}
	credential.Access, credential.AccountID = "fixture-new-key", "new-account"
	if _, err := h.store.SaveProviderCredential(ctx, "lmstudio", "api_key", "pockettrace", "work", credential); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog request did not complete")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "old-account-model") {
		t.Fatal("catalog exposed models from superseded credentials")
	}
}
