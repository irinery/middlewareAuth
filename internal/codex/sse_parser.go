package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/irinery/middlewareAuth/internal/security"
)

func ParseSSE(r io.Reader, maxEventBytes int) ([]CodexStreamEvent, error) {
	if maxEventBytes <= 0 {
		maxEventBytes = 1 << 20
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)
	var events []CodexStreamEvent
	var eventType string
	var data strings.Builder

	flush := func() error {
		payload := strings.TrimSpace(data.String())
		if payload == "" {
			eventType = ""
			data.Reset()
			return nil
		}
		if payload == "[DONE]" {
			events = append(events, CodexStreamEvent{Type: "done"})
			eventType = ""
			data.Reset()
			return nil
		}
		typ := eventType
		var parsed map[string]any
		if err := json.Unmarshal([]byte(payload), &parsed); err == nil {
			if text, ok := parsed["type"].(string); ok && text != "" {
				typ = text
			}
		}
		if typ == "" {
			typ = "message"
		}
		events = append(events, CodexStreamEvent{Type: typ, Payload: payload})
		eventType = ""
		data.Reset()
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, security.Wrap("ERR_CODEX_STREAM_INVALID", "stream SSE invalido", http.StatusBadGateway, err)
	}
	if data.Len() > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return events, nil
}
