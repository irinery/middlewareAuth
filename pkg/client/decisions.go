package client

import (
	"context"
	"net/http"
	"net/url"
)

type DecisionsService struct {
	client           *Client
	defaultProjectID string
}

func (s *DecisionsService) Providers(ctx context.Context, projectID, profileID string) (*DecisionProvidersResponse, error) {
	projectID = s.projectID(projectID)
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/decisions/providers"
	query := url.Values{}
	if profileID != "" {
		query.Set("profileId", profileID)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response DecisionProvidersResponse
	if err := s.client.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *DecisionsService) Login(ctx context.Context, request DecisionLoginRequest) (*DecisionStatus, error) {
	projectID := s.projectID(request.ProjectID)
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	var response DecisionStatus
	if err := s.client.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/decisions/login", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *DecisionsService) Status(ctx context.Context, projectID, providerID, profileID string) (*DecisionStatus, error) {
	projectID = s.projectID(projectID)
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	query := url.Values{"providerId": []string{providerID}}
	if profileID != "" {
		query.Set("profileId", profileID)
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/decisions/status?" + query.Encode()
	var response DecisionStatus
	if err := s.client.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *DecisionsService) Evaluate(ctx context.Context, request DecisionRequest) (*DecisionResponse, error) {
	projectID := s.projectID(request.ProjectID)
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	var response DecisionResponse
	if err := s.client.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/decisions/evaluate", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *DecisionsService) projectID(requested string) string {
	if requested != "" {
		return requested
	}
	return s.defaultProjectID
}
