package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"

	"github.com/irinery/middlewareAuth/internal/security"
)

func GenerateVerifier() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", security.Wrap("ERR_PKCE_GENERATION_FAILED", "falha ao gerar PKCE verifier", http.StatusInternalServerError, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func GenerateState() (string, error) {
	raw := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", security.Wrap("ERR_PKCE_GENERATION_FAILED", "falha ao gerar state OAuth", http.StatusInternalServerError, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
