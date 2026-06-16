package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/security"
)

func ResolveAuthIdentity(accessToken string, fallbackEmail string) (*AuthIdentity, error) {
	if len(accessToken) > 8192 {
		return nil, security.NewError("ERR_ACCESS_TOKEN_TOO_LARGE", "access token grande demais", http.StatusBadRequest)
	}
	identity := &AuthIdentity{Email: fallbackEmail}
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return identity, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity, nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return identity, security.Wrap("ERR_JWT_PAYLOAD_INVALID", "payload JWT invalido", http.StatusBadRequest, err)
	}
	identity.AccountID = firstClaimString(claims,
		"https://api.openai.com/auth.chatgpt_account_id",
		"https://api.openai.com/auth.chatgpt_user_id",
		"https://api.openai.com/auth.user_id",
		"chatgpt_account_id",
		"chatgpt_user_id",
		"chatgptAccountId",
		"account_id",
		"user_id",
		"userId",
		"sub",
	)
	if email := firstClaimString(claims, "email", "https://api.openai.com/auth.email"); email != "" {
		identity.Email = email
	}
	identity.ChatGPTPlanType = firstClaimString(claims,
		"https://api.openai.com/auth.chatgpt_plan_type",
		"chatgpt_plan_type",
		"chatgptPlanType",
	)
	identity.ProfileName = firstClaimString(claims, "name", "preferred_username")
	if exp, ok := claims["exp"].(float64); ok {
		identity.ExpiresAt = int64(exp) * 1000
	}
	return identity, nil
}

func firstClaimString(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := claims[key]; ok {
			if text, ok := value.(string); ok {
				return text
			}
		}
	}
	return ""
}
