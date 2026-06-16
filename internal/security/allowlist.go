package security

import (
	"net/http"
	"net/url"
	"strings"
)

func HostAllowed(rawURL string, allowed []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, item := range allowed {
		if host == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func ValidateAllowedURL(rawURL string, allowed []string) error {
	if HostAllowed(rawURL, allowed) {
		return nil
	}
	return NewError("ERR_HOST_NOT_ALLOWED", "host externo fora da allowlist", http.StatusBadRequest)
}
