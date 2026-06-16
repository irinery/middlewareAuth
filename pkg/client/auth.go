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
