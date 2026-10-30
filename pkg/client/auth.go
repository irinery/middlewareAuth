package client

import (
	"context"
	"net/http"
	"net/url"
)

type AuthService struct {
	client           *Client
	defaultProjectID string
}

// RotateClientToken replaces the server-wide client token and updates this
// client instance for subsequent requests. Persist the returned value in the
// caller's own credential storage before discarding it.
func (s *AuthService) RotateClientToken(ctx context.Context) (string, error) {
	var response struct {
		ClientToken string `json:"clientToken"`
	}
	if err := s.client.doJSON(ctx, http.MethodPost, "/v1/auth/client-token/rotate", nil, &response); err != nil {
		return "", err
	}
	if len(response.ClientToken) < 32 || len(response.ClientToken) > 2048 {
		return "", &ClientError{Code: "ERR_CLIENT_HTTP_FAILED", Message: "middleware retornou token interno invalido"}
	}
	s.client.tokenMu.Lock()
	s.client.middlewareToken = response.ClientToken
	s.client.tokenMu.Unlock()
	return response.ClientToken, nil
}

func (s *AuthService) StartOpenAILogin(ctx context.Context, request StartLoginRequest) (*LoginStartResponse, error) {
	projectID := request.ProjectID
	if projectID == "" {
		projectID = s.defaultProjectID
	}
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	var response LoginStartResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/auth/openai/login", request, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *AuthService) Refresh(ctx context.Context, request RefreshRequest) error {
	projectID := request.ProjectID
	if projectID == "" {
		projectID = s.defaultProjectID
	}
	if projectID == "" {
		return &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/auth/openai/refresh"
	if request.ProfileID != "" {
		path += "?profileId=" + url.QueryEscape(request.ProfileID)
	}
	return s.client.doJSON(ctx, http.MethodPost, path, nil, nil)
}
