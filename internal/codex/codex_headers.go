package codex

import (
	"net/http"
	"strings"
)

func BuildCodexHeaders(accessToken, accountID, originator string, additional []HeaderPair) http.Header {
	headers := make(http.Header)
	for _, pair := range additional {
		key := http.CanonicalHeaderKey(strings.TrimSpace(pair.Key))
		if key == "" || protectedHeader(key) {
			continue
		}
		headers.Set(key, pair.Value)
	}
	if originator == "" {
		originator = "middleware-codex-oauth"
	}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("chatgpt-account-id", accountID)
	headers.Set("originator", originator)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream, application/json")
	return headers
}

func protectedHeader(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "chatgpt-account-id", "originator", "content-type":
		return true
	default:
		return false
	}
}
