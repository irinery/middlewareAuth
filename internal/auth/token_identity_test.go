package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestResolveAuthIdentityExtractsAccountID(t *testing.T) {
	claims := map[string]any{
		"https://api.openai.com/auth.chatgpt_account_id": "acc-123",
		"email": "dev@example.com",
		"exp":   float64(2000000000),
	}
	payload, _ := json.Marshal(claims)
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	identity, err := ResolveAuthIdentity(token, "")
	if err != nil {
		t.Fatalf("ResolveAuthIdentity() error = %v", err)
	}
	if identity.AccountID != "acc-123" {
		t.Fatalf("AccountID = %q", identity.AccountID)
	}
	if identity.Email != "dev@example.com" {
		t.Fatalf("Email = %q", identity.Email)
	}
}

func TestResolveAuthIdentityUsesSubjectFallback(t *testing.T) {
	claims := map[string]any{
		"sub":   "user-123",
		"email": "dev@example.com",
	}
	payload, _ := json.Marshal(claims)
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	identity, err := ResolveAuthIdentity(token, "")
	if err != nil {
		t.Fatalf("ResolveAuthIdentity() error = %v", err)
	}
	if identity.AccountID != "user-123" {
		t.Fatalf("AccountID = %q", identity.AccountID)
	}
}

func TestResolveAuthIdentityUsesChatGPTUserID(t *testing.T) {
	claims := map[string]any{
		"chatgpt_user_id": "chatgpt-user-123",
	}
	payload, _ := json.Marshal(claims)
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	identity, err := ResolveAuthIdentity(token, "")
	if err != nil {
		t.Fatalf("ResolveAuthIdentity() error = %v", err)
	}
	if identity.AccountID != "chatgpt-user-123" {
		t.Fatalf("AccountID = %q", identity.AccountID)
	}
}

func TestResolveAuthIdentityReturnsPartialForNonJWT(t *testing.T) {
	identity, err := ResolveAuthIdentity("not-a-jwt", "fallback@example.com")
	if err != nil {
		t.Fatalf("ResolveAuthIdentity() error = %v", err)
	}
	if identity.Email != "fallback@example.com" {
		t.Fatalf("Email = %q", identity.Email)
	}
	if identity.AccountID != "" {
		t.Fatalf("AccountID = %q", identity.AccountID)
	}
}
