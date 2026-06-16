package httpapi

import (
	"net/http"
	"time"
)

type HealthCheckResult struct {
	Status string            `json:"status"`
	Checks []HealthCheckItem `json:"checks"`
}

type HealthCheckItem struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := HealthCheckResult{
		Status: "ok",
		Checks: []HealthCheckItem{
			{Name: "http", Status: "ok", LatencyMs: time.Since(start).Milliseconds()},
		},
	}
	writeJSON(w, http.StatusOK, result)
}
