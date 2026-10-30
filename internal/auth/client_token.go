package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/irinery/middlewareAuth/internal/security"
)

const clientTokenFile = "client-token.json"

// ClientTokenStore owns the single server-wide client token. The environment
// token is used only until the first rotation; the persisted digest wins after
// restarts so an old environment variable cannot re-enable a revoked token.
type ClientTokenStore struct {
	mu     sync.Mutex
	path   string
	digest [sha256.Size]byte
}

type clientTokenRecord struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

func NewClientTokenStore(stateDir, bootstrapToken string) (*ClientTokenStore, error) {
	s := &ClientTokenStore{path: filepath.Join(stateDir, clientTokenFile), digest: sha256.Sum256([]byte(bootstrapToken))}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 256 || info.Mode().Perm()&0o077 != 0 {
		return nil, security.NewError("ERR_CLIENT_TOKEN_STORE_INVALID", "estado do token interno invalido", http.StatusInternalServerError)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, security.Wrap("ERR_CLIENT_TOKEN_STORE_INVALID", "falha ao ler token interno", http.StatusInternalServerError, err)
	}
	var record clientTokenRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.Version != 1 || len(record.SHA256) != 2*sha256.Size {
		return nil, security.NewError("ERR_CLIENT_TOKEN_STORE_INVALID", "estado do token interno corrompido", http.StatusInternalServerError)
	}
	digest, err := hex.DecodeString(record.SHA256)
	if err != nil {
		return nil, security.NewError("ERR_CLIENT_TOKEN_STORE_INVALID", "digest do token interno invalido", http.StatusInternalServerError)
	}
	copy(s.digest[:], digest)
	return s, nil
}

func (s *ClientTokenStore) Valid(token string) bool {
	if token == "" || len(token) > 2048 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validLocked(token)
}

func (s *ClientTokenStore) Rotate(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validLocked(token) {
		return "", security.NewError("ERR_MIDDLEWARE_UNAUTHORIZED", "credencial interna invalida", http.StatusUnauthorized)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao gerar token interno", http.StatusInternalServerError, err)
	}
	next := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(next))
	if err := s.persist(digest); err != nil {
		return "", err
	}
	s.digest = digest
	return next, nil
}

func (s *ClientTokenStore) validLocked(token string) bool {
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], s.digest[:]) == 1
}

func (s *ClientTokenStore) persist(digest [sha256.Size]byte) error {
	raw, err := json.Marshal(clientTokenRecord{Version: 1, SHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao serializar token interno", http.StatusInternalServerError, err)
	}
	if info, err := os.Lstat(s.path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return security.NewError("ERR_CLIENT_TOKEN_STORE_INVALID", "estado do token interno invalido", http.StatusInternalServerError)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return security.Wrap("ERR_CLIENT_TOKEN_STORE_INVALID", "falha ao inspecionar token interno", http.StatusInternalServerError, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".client-token-*")
	if err != nil {
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao criar token temporario", http.StatusInternalServerError, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao proteger token temporario", http.StatusInternalServerError, err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao gravar token interno", http.StatusInternalServerError, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao sincronizar token interno", http.StatusInternalServerError, err)
	}
	if err := tmp.Close(); err != nil {
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao fechar token interno", http.StatusInternalServerError, err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return security.Wrap("ERR_CLIENT_TOKEN_ROTATION_FAILED", "falha ao ativar token interno", http.StatusInternalServerError, err)
	}
	_ = fsyncDir(filepath.Dir(s.path))
	return nil
}
