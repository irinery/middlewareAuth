package security

import (
	"regexp"
	"strings"
)

var (
	bearerRe      = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{16,}`)
	jwtRe         = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	skRe          = regexp.MustCompile(`\bsk-[A-Za-z0-9._~:+/=-]{8,}`)
	sensitiveJSON = regexp.MustCompile(`(?i)("?(?:access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|api[_-]?key|password|authorization|middleware[_-]?(?:secret[_-]?key|client[_-]?token))"?\s*:\s*)"[^"]*"`)
	sensitiveKV   = regexp.MustCompile(`(?i)\b((?:access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|api[_-]?key|password|authorization|middleware[_-]?(?:secret[_-]?key|client[_-]?token))=)[^&\s]+`)
)

func Redact(value string) string {
	if value == "" {
		return value
	}
	value = bearerRe.ReplaceAllString(value, "Bearer [REDACTED]")
	value = jwtRe.ReplaceAllString(value, "[REDACTED]")
	value = skRe.ReplaceAllString(value, "[REDACTED]")
	value = sensitiveJSON.ReplaceAllString(value, `${1}"[REDACTED]"`)
	value = sensitiveKV.ReplaceAllString(value, `${1}[REDACTED]`)
	return stripControl(value)
}

func stripControl(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' || r >= 32 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
