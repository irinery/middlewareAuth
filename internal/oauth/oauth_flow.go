package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

type AuthorizationFlow struct {
	Verifier    string
	Challenge   string
	State       string
	RedirectURI string
	URL         string
}

type OAuthCredentials struct {
	Access          string
	Refresh         string
	Expires         int64
	AccountID       string
	Email           string
	ChatGPTPlanType string
}

type OAuthLoginCallbacks struct {
	OnAuth     func(AuthPromptInfo) error
	OnPrompt   func(PromptInfo) (string, error)
	OnProgress func(string)
}

type AuthPromptInfo struct {
	URL          string
	Instructions string
}

type PromptInfo struct {
	Message string
}

type tokenEndpointResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email"`
}

func CreateAuthorizationFlow(ctx context.Context, cfg config.OAuthConfig, originator string) (*AuthorizationFlow, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	select {
	case <-ctx.Done():
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado", http.StatusRequestTimeout, ctx.Err())
	default:
	}
	if !security.ValidOriginator(originator) {
		return nil, security.NewError("ERR_INVALID_ORIGINATOR", "originator invalido", http.StatusBadRequest)
	}
	verifier, err := GenerateVerifier()
	if err != nil {
		return nil, err
	}
	state, err := GenerateState()
	if err != nil {
		return nil, err
	}
	redirectURI := config.RedirectURI(cfg)
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", cfg.ClientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", cfg.Scope)
	values.Set("code_challenge", ChallengeS256(verifier))
	values.Set("code_challenge_method", "S256")
	values.Set("state", state)
	if originator != "" {
		values.Set("originator", originator)
	}
	authURL := config.AuthorizeURL(cfg) + "?" + values.Encode()
	return &AuthorizationFlow{
		Verifier:    verifier,
		Challenge:   ChallengeS256(verifier),
		State:       state,
		RedirectURI: redirectURI,
		URL:         authURL,
	}, nil
}

func ExchangeAuthorizationCode(ctx context.Context, client *http.Client, cfg config.OAuthConfig, code, verifier, redirectURI string) (*OAuthCredentials, error) {
	if ctx == nil {
		return nil, security.NewError("ERR_CONTEXT_CANCELLED", "contexto ausente", http.StatusBadRequest)
	}
	if len(code) == 0 {
		return nil, security.NewError("ERR_OAUTH_MISSING_CODE", "authorization code ausente", http.StatusBadRequest)
	}
	if len(code) > 2000 {
		return nil, security.NewError("ERR_OAUTH_MISSING_CODE", "authorization code grande demais", http.StatusBadRequest)
	}
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL(cfg), bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_EXCHANGE_FAILED", "falha ao montar troca de token", http.StatusInternalServerError, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_EXCHANGE_FAILED", "falha ao trocar authorization code por token", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, security.Wrap("ERR_TOKEN_EXCHANGE_FAILED", "endpoint OAuth recusou a troca de token: "+security.Redact(string(body)), http.StatusBadGateway, nil)
	}
	var parsed tokenEndpointResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, security.Wrap("ERR_TOKEN_RESPONSE_INVALID", "resposta OAuth invalida", http.StatusBadGateway, err)
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" || parsed.ExpiresIn <= 0 {
		return nil, security.NewError("ERR_TOKEN_RESPONSE_INVALID", "resposta OAuth incompleta", http.StatusBadGateway)
	}
	return &OAuthCredentials{
		Access:    parsed.AccessToken,
		Refresh:   parsed.RefreshToken,
		Expires:   time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second).UnixMilli(),
		AccountID: parsed.AccountID,
		Email:     parsed.Email,
	}, nil
}

func LoginWithOAuthPKCE(ctx context.Context, cfg config.Config, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	if callbacks.OnAuth == nil || callbacks.OnPrompt == nil {
		return nil, security.NewError("ERR_OAUTH_CANCELLED", "callbacks OAuth obrigatorios ausentes", http.StatusBadRequest)
	}
	flow, err := CreateAuthorizationFlow(ctx, cfg.OAuth, "middleware-codex-oauth")
	if err != nil {
		return nil, err
	}
	if err := callbacks.OnAuth(AuthPromptInfo{URL: flow.URL, Instructions: "abra a URL e conclua o login"}); err != nil {
		return nil, security.Wrap("ERR_OAUTH_CANCELLED", "login OAuth cancelado", http.StatusRequestTimeout, err)
	}
	code, err := callbacks.OnPrompt(PromptInfo{Message: "cole o authorization code ou redirect URL"})
	if err != nil {
		return nil, security.Wrap("ERR_OAUTH_CANCELLED", "login OAuth cancelado", http.StatusRequestTimeout, err)
	}
	code = extractCode(code)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return ExchangeAuthorizationCode(ctx, config.NewHTTPClient(cfg.Codex), cfg.OAuth, code, flow.Verifier, flow.RedirectURI)
}

func extractCode(input string) string {
	if parsed, err := url.Parse(input); err == nil {
		if code := parsed.Query().Get("code"); code != "" {
			return code
		}
	}
	return input
}

func expiresMillis(seconds int64) int64 {
	if seconds <= 0 {
		seconds = 3600
	}
	return time.Now().Add(time.Duration(seconds) * time.Second).UnixMilli()
}

func atoiDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
