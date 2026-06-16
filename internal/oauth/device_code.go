package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/security"
)

type DeviceCodePrompt struct {
	VerificationURL string
	UserCode        string
	ExpiresInMs     int64
}

type RequestedDeviceCode struct {
	DeviceAuthID    string
	UserCode        string
	VerificationURL string
	IntervalMs      int64
	ExpiresInMs     int64
}

type DeviceCodeCallbacks struct {
	OnVerification func(DeviceCodePrompt) error
	OnProgress     func(string)
}

func LoginWithDeviceCode(ctx context.Context, cfg config.Config, callbacks DeviceCodeCallbacks) (*OAuthCredentials, error) {
	if callbacks.OnVerification == nil {
		return nil, security.NewError("ERR_DEVICE_CODE_REQUEST_FAILED", "callback de verificacao ausente", http.StatusBadRequest)
	}
	client := config.NewHTTPClient(cfg.Codex)
	requested, err := RequestDeviceCode(ctx, client, cfg.OAuth)
	if err != nil {
		return nil, err
	}
	if err := callbacks.OnVerification(DeviceCodePrompt{
		VerificationURL: requested.VerificationURL,
		UserCode:        requested.UserCode,
		ExpiresInMs:     requested.ExpiresInMs,
	}); err != nil {
		return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "device-code cancelado", http.StatusRequestTimeout, err)
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("aguardando autorizacao device-code")
	}
	return PollDeviceCode(ctx, client, cfg.OAuth, *requested)
}

func RequestDeviceCode(ctx context.Context, client *http.Client, cfg config.OAuthConfig) (*RequestedDeviceCode, error) {
	payload := map[string]string{"client_id": cfg.ClientID}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(cfg.AuthBaseURL, "/") + "/api/accounts/deviceauth/usercode"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, security.Wrap("ERR_DEVICE_CODE_REQUEST_FAILED", "falha ao montar request device-code", http.StatusInternalServerError, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, security.Wrap("ERR_DEVICE_CODE_REQUEST_FAILED", "falha ao solicitar device-code", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, security.Wrap("ERR_DEVICE_CODE_REQUEST_FAILED", security.Redact(string(raw)), http.StatusBadGateway, nil)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, security.Wrap("ERR_DEVICE_CODE_RESPONSE_INVALID", "resposta device-code invalida", http.StatusBadGateway, err)
	}
	deviceID := firstString(parsed, "device_authorization_id", "device_auth_id", "deviceAuthId", "id")
	userCode := firstString(parsed, "user_code", "userCode")
	verification := firstString(parsed, "verification_uri", "verification_url", "verificationUrl")
	if verification == "" {
		verification = strings.TrimRight(cfg.AuthBaseURL, "/") + "/codex/device"
	}
	intervalMs := int64(firstNumber(parsed, 5000, "interval_ms", "intervalMs"))
	if interval := firstNumber(parsed, 0, "interval"); interval > 0 {
		intervalMs = int64(interval * 1000)
	}
	expiresMs := int64(firstNumber(parsed, 900000, "expires_in_ms", "expiresInMs"))
	if expires := firstNumber(parsed, 0, "expires_in", "expiresIn"); expires > 0 {
		expiresMs = int64(expires * 1000)
	}
	if deviceID == "" || userCode == "" {
		return nil, security.NewError("ERR_DEVICE_CODE_RESPONSE_INVALID", "resposta device-code incompleta", http.StatusBadGateway)
	}
	return &RequestedDeviceCode{
		DeviceAuthID:    deviceID,
		UserCode:        userCode,
		VerificationURL: verification,
		IntervalMs:      intervalMs,
		ExpiresInMs:     expiresMs,
	}, nil
}

func PollDeviceCode(ctx context.Context, client *http.Client, cfg config.OAuthConfig, device RequestedDeviceCode) (*OAuthCredentials, error) {
	if device.IntervalMs < 1000 {
		device.IntervalMs = 5000
	}
	deadline := time.NewTimer(time.Duration(device.ExpiresInMs) * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Duration(device.IntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		credentials, pending, err := pollDeviceOnce(ctx, client, cfg, device)
		if err != nil {
			return nil, err
		}
		if !pending {
			return credentials, nil
		}
		select {
		case <-ctx.Done():
			return nil, security.Wrap("ERR_CONTEXT_CANCELLED", "contexto cancelado durante device-code", http.StatusRequestTimeout, ctx.Err())
		case <-deadline.C:
			return nil, security.NewError("ERR_DEVICE_CODE_TIMEOUT", "device-code expirou", http.StatusRequestTimeout)
		case <-ticker.C:
		}
	}
}

func pollDeviceOnce(ctx context.Context, client *http.Client, cfg config.OAuthConfig, device RequestedDeviceCode) (*OAuthCredentials, bool, error) {
	payload := map[string]string{"device_authorization_id": device.DeviceAuthID, "client_id": cfg.ClientID}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(cfg.AuthBaseURL, "/") + "/api/accounts/deviceauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, security.Wrap("ERR_DEVICE_CODE_EXCHANGE_FAILED", "falha ao montar polling device-code", http.StatusInternalServerError, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, security.Wrap("ERR_DEVICE_CODE_EXCHANGE_FAILED", "falha no polling device-code", http.StatusBadGateway, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, false, security.Wrap("ERR_DEVICE_CODE_EXCHANGE_FAILED", security.Redact(string(raw)), http.StatusBadGateway, nil)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, false, security.Wrap("ERR_DEVICE_CODE_RESPONSE_INVALID", "resposta de polling invalida", http.StatusBadGateway, err)
	}
	if access := firstString(parsed, "access_token", "accessToken"); access != "" {
		refresh := firstString(parsed, "refresh_token", "refreshToken")
		expires := int64(firstNumber(parsed, 3600, "expires_in", "expiresIn"))
		if refresh == "" {
			return nil, false, security.NewError("ERR_DEVICE_CODE_RESPONSE_INVALID", "resposta device-code sem refresh token", http.StatusBadGateway)
		}
		return &OAuthCredentials{Access: access, Refresh: refresh, Expires: expiresMillis(expires)}, false, nil
	}
	code := firstString(parsed, "authorization_code", "authorizationCode", "code")
	if code == "" {
		return nil, false, security.NewError("ERR_DEVICE_CODE_RESPONSE_INVALID", "resposta device-code sem authorization_code", http.StatusBadGateway)
	}
	flow, err := CreateAuthorizationFlow(ctx, cfg, "middleware-codex-oauth")
	if err != nil {
		return nil, false, err
	}
	credentials, err := ExchangeAuthorizationCode(ctx, client, cfg, code, flow.Verifier, flow.RedirectURI)
	return credentials, false, err
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text, ok := value.(string); ok {
				return text
			}
		}
	}
	return ""
}

func firstNumber(values map[string]any, fallback int, keys ...string) int {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			switch typed := value.(type) {
			case float64:
				return int(typed)
			case int:
				return typed
			case string:
				return atoiDefault(typed, fallback)
			}
		}
	}
	return fallback
}
