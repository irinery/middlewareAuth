package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	storeFileName = "auth-profiles.json"
	maxStoreBytes = 5 << 20
)

type ProfileStore interface {
	SaveAuthProfile(ctx context.Context, projectID string, profileID string, credential StoredOAuthCredential) (*AuthProfileSaveResult, error)
	LoadAuthProfile(ctx context.Context, projectID string, profileID string) (*StoredOAuthCredential, error)
	SaveProviderCredential(ctx context.Context, providerID string, credentialType string, projectID string, profileID string, credential StoredOAuthCredential) (*AuthProfileSaveResult, error)
	LoadProviderCredential(ctx context.Context, providerID string, projectID string, profileID string) (*StoredOAuthCredential, error)
}

type FileStore struct {
	path      string
	secret    string
	locks     *lockSet
	storeLock *lockSet
}

type AuthProfileStore struct {
	Version   int                 `json:"version"`
	Profiles  []AuthProfileRecord `json:"profiles"`
	UpdatedAt int64               `json:"updatedAt"`
}

type AuthProfileRecord struct {
	ProjectID  string                  `json:"projectId"`
	ProfileID  string                  `json:"profileId"`
	Provider   string                  `json:"provider"`
	Type       string                  `json:"type"`
	Credential EncryptedCredentialBlob `json:"credential"`
	Metadata   AuthProfileMetadata     `json:"metadata"`
}

type AuthProfileMetadata struct {
	CreatedAt  int64 `json:"createdAt"`
	UpdatedAt  int64 `json:"updatedAt"`
	LastUsedAt int64 `json:"lastUsedAt,omitempty"`
}

func NewFileStore(cfg config.Config) *FileStore {
	return &FileStore{
		path:      filepath.Join(cfg.StateDir, storeFileName),
		secret:    cfg.Security.SecretKey,
		locks:     newLockSet(),
		storeLock: newLockSet(),
	}
}

func (s *FileStore) SaveAuthProfile(ctx context.Context, projectID string, profileID string, credential StoredOAuthCredential) (*AuthProfileSaveResult, error) {
	return s.SaveProviderCredential(ctx, "openai", "oauth", projectID, profileID, credential)
}

func (s *FileStore) SaveProviderCredential(ctx context.Context, providerID string, credentialType string, projectID string, profileID string, credential StoredOAuthCredential) (*AuthProfileSaveResult, error) {
	profileID = security.NormalizeProfileID(profileID)
	providerID = normalizeProviderID(providerID)
	if credentialType == "" {
		credentialType = "api_key"
	}
	if err := validateProviderProfile(providerID, credentialType, projectID, profileID, credential); err != nil {
		return nil, err
	}
	credential.Provider = providerID
	release, err := s.locks.acquire(ctx, providerProfileKey(providerID, projectID, profileID), 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer release()
	releaseStore, err := s.storeLock.acquire(ctx, "__auth_store__", 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer releaseStore()

	store, err := s.readStore()
	if err != nil {
		return nil, err
	}
	blob, err := EncryptCredential(s.secret, credential)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	found := false
	for i := range store.Profiles {
		record := &store.Profiles[i]
		if recordMatchesProvider(*record, providerID, projectID, profileID) {
			record.Credential = *blob
			record.Metadata.UpdatedAt = now
			record.Provider = providerID
			record.Type = credentialType
			found = true
			break
		}
	}
	if !found {
		if len(store.Profiles) >= 1000 {
			return nil, security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "limite de perfis atingido", http.StatusConflict)
		}
		store.Profiles = append(store.Profiles, AuthProfileRecord{
			ProjectID:  projectID,
			ProfileID:  profileID,
			Provider:   providerID,
			Type:       credentialType,
			Credential: *blob,
			Metadata: AuthProfileMetadata{
				CreatedAt: now,
				UpdatedAt: now,
			},
		})
	}
	store.UpdatedAt = now
	if err := s.writeStore(store); err != nil {
		return nil, err
	}
	return &AuthProfileSaveResult{ProjectID: projectID, ProfileID: profileID, SavedAt: now}, nil
}

func (s *FileStore) LoadAuthProfile(ctx context.Context, projectID string, profileID string) (*StoredOAuthCredential, error) {
	return s.LoadProviderCredential(ctx, "openai", projectID, profileID)
}

func (s *FileStore) LoadProviderCredential(ctx context.Context, providerID string, projectID string, profileID string) (*StoredOAuthCredential, error) {
	providerID = normalizeProviderID(providerID)
	profileID = security.NormalizeProfileID(profileID)
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	select {
	case <-ctx.Done():
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado", http.StatusRequestTimeout, ctx.Err())
	default:
	}
	if !security.ValidProjectID(projectID) {
		return nil, security.NewError("ERR_INVALID_PROJECT_ID", "projectId invalido", http.StatusBadRequest)
	}
	if !validProviderID(providerID) {
		return nil, security.NewError("ERR_INVALID_PROVIDER_ID", "providerId invalido", http.StatusBadRequest)
	}
	if !security.ValidProfileID(profileID) {
		return nil, security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest)
	}
	store, err := s.readStore()
	if err != nil {
		return nil, err
	}
	for _, record := range store.Profiles {
		if recordMatchesProvider(record, providerID, projectID, profileID) {
			credential, err := DecryptCredential(s.secret, record.Credential)
			if err != nil {
				return nil, err
			}
			credential.Provider = providerID
			return credential, nil
		}
	}
	return nil, security.NewError("ERR_AUTH_PROFILE_NOT_FOUND", "perfil de autenticacao nao encontrado", http.StatusNotFound)
}

func (s *FileStore) readStore() (*AuthProfileStore, error) {
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &AuthProfileStore{Version: 1, Profiles: []AuthProfileRecord{}, UpdatedAt: time.Now().UnixMilli()}, nil
	}
	if err != nil {
		return nil, security.Wrap("ERR_AUTH_STORE_CORRUPTED", "store indisponivel", http.StatusInternalServerError, err)
	}
	if info.Size() > maxStoreBytes {
		return nil, security.NewError("ERR_AUTH_STORE_CORRUPTED", "store excede tamanho maximo", http.StatusInternalServerError)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, security.Wrap("ERR_AUTH_STORE_CORRUPTED", "falha ao ler store", http.StatusInternalServerError, err)
	}
	var store AuthProfileStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, security.Wrap("ERR_AUTH_STORE_CORRUPTED", "store corrompido", http.StatusInternalServerError, err)
	}
	if store.Version == 0 {
		return nil, security.NewError("ERR_AUTH_STORE_CORRUPTED", "schema do store invalido", http.StatusInternalServerError)
	}
	if store.Profiles == nil {
		store.Profiles = []AuthProfileRecord{}
	}
	return &store, nil
}

func (s *FileStore) writeStore(store *AuthProfileStore) error {
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao serializar store", http.StatusInternalServerError, err)
	}
	if len(raw) > maxStoreBytes {
		return security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "store excede tamanho maximo", http.StatusConflict)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao criar state dir", http.StatusInternalServerError, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auth-profiles-*.tmp")
	if err != nil {
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao criar arquivo temporario", http.StatusInternalServerError, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao ajustar permissao do store", http.StatusInternalServerError, err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao escrever store temporario", http.StatusInternalServerError, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao sincronizar store temporario", http.StatusInternalServerError, err)
	}
	if err := tmp.Close(); err != nil {
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao fechar store temporario", http.StatusInternalServerError, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return security.Wrap("ERR_AUTH_STORE_WRITE_FAILED", "falha ao promover store temporario", http.StatusInternalServerError, err)
	}
	_ = fsyncDir(filepath.Dir(s.path))
	return nil
}

func fsyncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func validateProfile(projectID string, profileID string, credential StoredOAuthCredential) error {
	return validateProviderProfile("openai", "oauth", projectID, profileID, credential)
}

func validateProviderProfile(providerID string, credentialType string, projectID string, profileID string, credential StoredOAuthCredential) error {
	if !validProviderID(providerID) {
		return security.NewError("ERR_INVALID_PROVIDER_ID", "providerId invalido", http.StatusBadRequest)
	}
	if !security.ValidProjectID(projectID) {
		return security.NewError("ERR_INVALID_PROJECT_ID", "projectId invalido", http.StatusBadRequest)
	}
	if !security.ValidProfileID(profileID) {
		return security.NewError("ERR_INVALID_PROFILE_ID", "profileId invalido", http.StatusBadRequest)
	}
	switch credentialType {
	case "oauth":
		if credential.Access == "" || credential.Refresh == "" || credential.Expires == 0 || credential.AccountID == "" {
			return security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "credencial OAuth incompleta", http.StatusBadRequest)
		}
	case "api_key":
		if credential.Access == "" || credential.AccountID == "" {
			return security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "credencial API key incompleta", http.StatusBadRequest)
		}
	default:
		return security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "tipo de credencial invalido", http.StatusBadRequest)
	}
	return nil
}

func recordMatchesProvider(record AuthProfileRecord, providerID string, projectID string, profileID string) bool {
	recordProvider := normalizeProviderID(record.Provider)
	if recordProvider == "" {
		recordProvider = "openai"
	}
	return recordProvider == providerID && record.ProjectID == projectID && record.ProfileID == profileID
}

func providerProfileKey(providerID string, projectID string, profileID string) string {
	return normalizeProviderID(providerID) + ":" + profileKey(projectID, profileID)
}

func normalizeProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func validProviderID(providerID string) bool {
	if providerID == "" {
		return false
	}
	return security.ValidOriginator(providerID)
}
