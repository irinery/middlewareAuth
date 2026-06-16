package security

import (
	"strings"
	"testing"
)

func TestRedactSensitiveJSONFields(t *testing.T) {
	input := `{"access_token":"access-secret","refresh_token":"refresh-secret","client_secret":"client-secret","ok":"keep"}`
	got := Redact(input)
	for _, leaked := range []string{"access-secret", "refresh-secret", "client-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("Redact leaked %q in %s", leaked, got)
		}
	}
	if !strings.Contains(got, `"ok":"keep"`) {
		t.Fatalf("Redact changed safe field: %s", got)
	}
}

func TestRedactSensitiveFormAndEnvFields(t *testing.T) {
	input := "refresh_token=refresh-secret&access_token=access-secret MIDDLEWARE_SECRET_KEY=secret-secret MIDDLEWARE_CLIENT_TOKEN=client-token"
	got := Redact(input)
	for _, leaked := range []string{"refresh-secret", "access-secret", "secret-secret", "client-token"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("Redact leaked %q in %s", leaked, got)
		}
	}
}

func TestRedactBearerJWTAndOpenAIKey(t *testing.T) {
	bearer := "abcdefghijklmnopqrstuvwxyz" + "012345"
	jwt := "eyJhbGciOiJub25lIn0" + "." + "eyJzdWIiOiIxMjM0NTY3OCJ9" + "." + "signature"
	openAIKey := "sk-" + "abcdefghijklmnopqrstuvwxyz"
	lmStudioKey := "sk-lm-" + "abc123:def456ghi789"
	input := "Authorization: Bearer " + bearer + " token " + jwt + " " + openAIKey + " " + lmStudioKey
	got := Redact(input)
	for _, leaked := range []string{bearer, jwt, openAIKey, lmStudioKey} {
		if strings.Contains(got, leaked) {
			t.Fatalf("Redact leaked %q in %s", leaked, got)
		}
	}
}
