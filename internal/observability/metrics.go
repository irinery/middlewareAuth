package observability

import "sync/atomic"

type Metrics struct {
	codexRequests atomic.Int64
	codexErrors   atomic.Int64
	refreshes     atomic.Int64
}

func (m *Metrics) IncCodexRequest() {
	m.codexRequests.Add(1)
}

func (m *Metrics) IncCodexError() {
	m.codexErrors.Add(1)
}

func (m *Metrics) IncRefresh() {
	m.refreshes.Add(1)
}

func (m *Metrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"codexRequests": m.codexRequests.Load(),
		"codexErrors":   m.codexErrors.Load(),
		"refreshes":     m.refreshes.Load(),
	}
}
