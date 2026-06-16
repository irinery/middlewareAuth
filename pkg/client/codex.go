package client

import (
	"context"
	"net/http"
	"net/url"
)

type CodexService struct {
	client           *Client
	defaultProjectID string
}

func (s *CodexService) Responses(ctx context.Context, projectID string, profileID string, request CodexResponseRequest) (*CodexResponseStream, error) {
	if projectID == "" {
		projectID = s.defaultProjectID
	}
	if projectID == "" {
		return nil, &ClientError{Code: "ERR_INVALID_PROJECT_ID", Message: "projectID obrigatorio"}
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/codex/responses"
	if profileID != "" {
		path += "?profileId=" + url.QueryEscape(profileID)
	}
	var response CodexResponseStream
	err := s.client.doJSON(ctx, http.MethodPost, path, request, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}
