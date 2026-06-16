package auth

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/irinery/middlewareAuth/internal/security"
)

type lockSet struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newLockSet() *lockSet {
	return &lockSet{locks: make(map[string]chan struct{})}
}

func (l *lockSet) acquire(ctx context.Context, key string, timeout time.Duration) (func(), error) {
	l.mu.Lock()
	ch := l.locks[key]
	if ch == nil {
		ch = make(chan struct{}, 1)
		ch <- struct{}{}
		l.locks[key] = ch
	}
	l.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado ao aguardar lock", http.StatusRequestTimeout, ctx.Err())
	case <-timer.C:
		return nil, security.NewError("ERR_AUTH_STORE_LOCK_TIMEOUT", "timeout ao adquirir lock do perfil", http.StatusConflict)
	case <-ch:
		return func() { ch <- struct{}{} }, nil
	}
}

func profileKey(projectID, profileID string) string {
	return projectID + ":" + profileID
}
