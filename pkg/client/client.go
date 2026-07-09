package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL         string
	middlewareToken string
	httpClient      *http.Client
	timeout         time.Duration

	Auth  *AuthService
	Codex *CodexService
}

func NewClient(options ClientOptions) (*Client, error) {
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		return nil, &ClientError{Code: "ERR_CLIENT_BASE_URL_INVALID", Message: "BaseURL obrigatoria"}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, &ClientError{Code: "ERR_CLIENT_BASE_URL_INVALID", Message: "BaseURL invalida"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &ClientError{Code: "ERR_CLIENT_BASE_URL_INVALID", Message: "BaseURL precisa ser http ou https"}
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &ClientError{Code: "ERR_CLIENT_BASE_URL_INVALID", Message: "BaseURL nao pode conter userinfo, query ou fragmento"}
	}
	middlewareToken := strings.TrimSpace(options.MiddlewareToken)
	if len(middlewareToken) < 32 || len(middlewareToken) > 2048 {
		return nil, &ClientError{Code: "ERR_CLIENT_AUTH_TOKEN_MISSING", Message: "MiddlewareToken precisa ter entre 32 e 2048 caracteres"}
	}
	timeoutMs := options.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = 60000
	}
	if timeoutMs < 1000 {
		timeoutMs = 1000
	}
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c := &Client{
		baseURL:         strings.TrimRight(parsed.String(), "/"),
		middlewareToken: middlewareToken,
		httpClient:      &clientCopy,
		timeout:         time.Duration(timeoutMs) * time.Millisecond,
	}
	c.Auth = &AuthService{client: c, defaultProjectID: options.ProjectID}
	c.Codex = &CodexService{client: c, defaultProjectID: options.ProjectID}
	return c, nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	if ctx == nil {
		return &ClientError{Code: "ERR_CONTEXT_CANCELLED", Message: "contexto obrigatorio"}
	}
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: "falha ao serializar payload"}
		}
	}
	maxRetries := 0
	if retryableMethod(method) {
		maxRetries = 2
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		req, err := http.NewRequestWithContext(reqCtx, method, c.url(path), bytes.NewReader(raw))
		if err != nil {
			cancel()
			return &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: err.Error()}
		}
		req.Header.Set("Authorization", "Bearer "+c.middlewareToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return &ClientError{Code: "ERR_CONTEXT_CANCELLED", Message: ctx.Err().Error()}
			}
			lastErr = &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: err.Error()}
		} else {
			err = decodeResponse(resp, out)
			cancel()
			if err == nil {
				return nil
			}
			lastErr = err
			if ce, ok := err.(*ClientError); ok {
				if ce.Status != 502 && ce.Status != 503 && ce.Status != 504 {
					return ce
				}
			}
		}
		if attempt < maxRetries {
			timer := time.NewTimer(time.Duration(500*(1<<attempt)) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return &ClientError{Code: "ERR_CONTEXT_CANCELLED", Message: ctx.Err().Error()}
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func retryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (c *Client) url(path string) string {
	return c.baseURL + "/" + strings.TrimLeft(path, "/")
}

func decodeResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: "falha ao ler resposta do middleware", Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ce := &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: fmt.Sprintf("middleware respondeu HTTP %d", resp.StatusCode), Status: resp.StatusCode}
		var wrapped struct {
			Error ClientError `json:"error"`
		}
		if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Error.Code != "" {
			ce.Code = wrapped.Error.Code
			ce.Message = wrapped.Error.Message
			ce.RetryAfterMs = wrapped.Error.RetryAfterMs
		}
		if resp.StatusCode == http.StatusUnauthorized && ce.Code == "ERR_CLIENT_HTTP_FAILED" {
			ce.Code = "ERR_MIDDLEWARE_UNAUTHORIZED"
			ce.Message = "middleware recusou autenticacao"
		}
		if resp.StatusCode == http.StatusTooManyRequests && ce.RetryAfterMs == 0 {
			ce.RetryAfterMs = retryAfterMs(resp)
		}
		return ce
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: "resposta invalida do middleware", Status: resp.StatusCode}
	}
	return nil
}

func retryAfterMs(resp *http.Response) int {
	if value := resp.Header.Get("retry-after-ms"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	if value := resp.Header.Get("Retry-After"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed * 1000
		}
	}
	return 0
}
