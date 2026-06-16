package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestRequestDeviceCodeParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_authorization_id":"dev-1","user_code":"ABCD","verification_uri":"https://auth.openai.com/codex/device","interval":1,"expires_in":900}`))
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
