package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

type Refresher struct {
	cfg    config.Config
	store  ProfileStore
	client *http.Client
	locks  *lockSet
}

type refreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func NewRefresher(cfg config.Config, store ProfileStore, client *http.Client) *Refresher {
	if client == nil {
		client = config.NewHTTPClient(cfg.Codex)
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Refresher{cfg: cfg, store: store, client: &copy, locks: newLockSet()}
}

func (r *Refresher) ResolveFreshCredential(ctx context.Context, projectID string, profileID string, minTTLms int64) (*StoredOAuthCredential, error) {
	profileID = security.NormalizeProfileID(profileID)
	if minTTLms <= 0 {
		minTTLms = 60000
	}
	current, err := r.store.LoadAuthProfile(ctx, projectID, profileID)
	if err != nil {
		return nil, err
	}
	if current.Expires > time.Now().Add(time.Duration(minTTLms)*time.Millisecond).UnixMilli() {
		return current, nil
	}
	release, err := r.locks.acquire(ctx, profileKey(projectID, profileID), 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer release()

	current, err = r.store.LoadAuthProfile(ctx, projectID, profileID)
	if err != nil {
		return nil, err
	}
	if current.Expires > time.Now().Add(time.Duration(minTTLms)*time.Millisecond).UnixMilli() {
		return current, nil
	}
	updated, err := r.refresh(ctx, *current)
	if err != nil {
		return nil, err
	}
	if current.AccountID != "" && updated.AccountID != "" && current.AccountID != updated.AccountID {
		return nil, security.NewError("ERR_ACCOUNT_ID_CHANGED", "accountId mudou durante refresh", http.StatusConflict)
	}
	if updated.AccountID == "" {
		updated.AccountID = current.AccountID
	}
	if updated.Email == "" {
		updated.Email = current.Email
	}
	if updated.ChatGPTPlanType == "" {
		updated.ChatGPTPlanType = current.ChatGPTPlanType
	}
	if _, err := r.store.SaveAuthProfile(ctx, projectID, profileID, *updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (r *Refresher) refresh(ctx context.Context, credential StoredOAuthCredential) (*StoredOAuthCredential, error) {
	if credential.Refresh == "" {
		return nil, security.NewError("ERR_TOKEN_REFRESH_FAILED", "refresh token ausente", http.StatusUnauthorized)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", credential.Refresh)
	form.Set("client_id", r.cfg.OAuth.ClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL(r.cfg.OAuth), bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_REFRESH_FAILED", "falha ao montar refresh", http.StatusInternalServerError, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_REFRESH_FAILED", "falha ao renovar token", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, security.Wrap("ERR_TOKEN_REFRESH_FAILED", "endpoint OAuth recusou refresh: "+security.Redact(string(raw)), http.StatusBadGateway, nil)
	}
	var parsed refreshTokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, security.Wrap("ERR_TOKEN_RESPONSE_INVALID", "resposta de refresh invalida", http.StatusBadGateway, err)
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" || parsed.ExpiresIn <= 0 {
		return nil, security.NewError("ERR_TOKEN_RESPONSE_INVALID", "resposta de refresh incompleta", http.StatusBadGateway)
	}
	identity, _ := ResolveAuthIdentity(parsed.AccessToken, credential.Email)
	accountID := identity.AccountID
	if accountID == "" {
		accountID = credential.AccountID
	}
	return &StoredOAuthCredential{
		Access:          parsed.AccessToken,
		Refresh:         parsed.RefreshToken,
		Expires:         time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second).UnixMilli(),
		AccountID:       accountID,
		Email:           identity.Email,
		ChatGPTPlanType: identity.ChatGPTPlanType,
	}, nil
}
