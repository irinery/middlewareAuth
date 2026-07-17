package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

func TestCreateAuthorizationFlowBuildsPKCEURL(t *testing.T) {
	cfg := config.OAuthConfig{
		ClientID:      config.DefaultClientID,
		AuthBaseURL:   "https://auth.openai.com",
		AuthorizePath: "/oauth/authorize",
		TokenPath:     "/oauth/token",
		CallbackHost:  "localhost",
		CallbackPort:  18787,
		CallbackPath:  "/v1/auth/openai/callback",
		Scope:         "openid profile email offline_access",
	}
	flow, err := CreateAuthorizationFlow(context.Background(), cfg, "middleware-codex-oauth")
	if err != nil {
		t.Fatalf("CreateAuthorizationFlow() error = %v", err)
	}
	parsed, err := url.Parse(flow.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for _, key := range []string{"response_type", "client_id", "redirect_uri", "scope", "code_challenge", "code_challenge_method", "state"} {
		if query.Get(key) == "" {
			t.Fatalf("missing query param %s in %s", key, flow.URL)
		}
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %s", query.Get("code_challenge_method"))
	}
	if query.Get("redirect_uri") != "http://localhost:18787/v1/auth/openai/callback" {
		t.Fatalf("redirect_uri = %s", query.Get("redirect_uri"))
	}
	if flow.Verifier == "" || flow.Challenge == "" || flow.State == "" {
		t.Fatalf("flow incomplete: %#v", flow)
	}
}

func TestExchangeAuthorizationCodeRejectsMissingCode(t *testing.T) {
	_, err := ExchangeAuthorizationCode(context.Background(), http.DefaultClient, config.OAuthConfig{}, "", "verifier", "http://localhost/callback")
	if security.Code(err) != "ERR_OAUTH_MISSING_CODE" {
		t.Fatalf("code = %s, want ERR_OAUTH_MISSING_CODE (%v)", security.Code(err), err)
	}
}

func TestExchangeAuthorizationCodeDoesNotFollowRedirect(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	_, err := ExchangeAuthorizationCode(context.Background(), source.Client(), config.OAuthConfig{
		ClientID:    config.DefaultClientID,
		AuthBaseURL: source.URL,
		TokenPath:   "/oauth/token",
	}, "authorization-code", "verifier", "http://localhost:18787/v1/auth/openai/callback")
	if err == nil {
		t.Fatal("esperava erro para redirect OAuth")
	}
	if targetCalled {
		t.Fatal("troca OAuth seguiu redirect")
	}
}

func TestRequestDeviceCodeParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD","interval":"1"}`))
	}))
	defer server.Close()

	device, err := RequestDeviceCode(context.Background(), server.Client(), config.OAuthConfig{
		ClientID:    config.DefaultClientID,
		AuthBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("RequestDeviceCode() error = %v", err)
	}
	if device.DeviceAuthID != "dev-1" || device.UserCode != "ABCD" || device.IntervalMs != 1000 {
		t.Fatalf("device = %#v", device)
	}
	if device.VerificationURL != server.URL+"/codex/device" {
		t.Fatalf("verification URL = %q", device.VerificationURL)
	}
}

func TestPollDeviceCodeUsesProviderPKCEContract(t *testing.T) {
	var pollRequests int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/token":
			pollRequests++
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["device_auth_id"] != "dev-1" || payload["user_code"] != "ABCD" {
				t.Fatalf("poll payload = %#v", payload)
			}
			if _, exists := payload["device_authorization_id"]; exists {
				t.Fatalf("poll payload retained obsolete field: %#v", payload)
			}
			if pollRequests == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "authorization-code",
				"code_challenge":     "provider-code-challenge",
				"code_verifier":      "provider-code-verifier",
			})
		case "/oauth/token":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			if form.Get("code") != "authorization-code" {
				t.Fatalf("authorization code = %q", form.Get("code"))
			}
			if form.Get("code_verifier") != "provider-code-verifier" {
				t.Fatalf("code verifier = %q", form.Get("code_verifier"))
			}
			if form.Get("redirect_uri") != server.URL+"/deviceauth/callback" {
				t.Fatalf("redirect URI = %q", form.Get("redirect_uri"))
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	credentials, err := PollDeviceCode(context.Background(), server.Client(), config.OAuthConfig{
		ClientID:    config.DefaultClientID,
		AuthBaseURL: server.URL,
		TokenPath:   "/oauth/token",
	}, RequestedDeviceCode{
		DeviceAuthID: "dev-1",
		UserCode:     "ABCD",
		IntervalMs:   1000,
		ExpiresInMs:  3000,
	})
	if err != nil {
		t.Fatalf("PollDeviceCode() error = %v", err)
	}
	if pollRequests != 2 {
		t.Fatalf("poll requests = %d", pollRequests)
	}
	if credentials.Access != "access-token" || credentials.Refresh != "refresh-token" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestPollDeviceCodeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pending", http.StatusForbidden)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := PollDeviceCode(ctx, server.Client(), config.OAuthConfig{
		ClientID:    config.DefaultClientID,
		AuthBaseURL: server.URL,
	}, RequestedDeviceCode{DeviceAuthID: "dev-1", UserCode: "ABCD", IntervalMs: 1, ExpiresInMs: 1})
	if security.Code(err) != "ERR_DEVICE_CODE_TIMEOUT" && security.Code(err) != "ERR_CONTEXT_CANCELLED" {
		t.Fatalf("code = %s, want timeout/cancelled (%v)", security.Code(err), err)
	}
}
