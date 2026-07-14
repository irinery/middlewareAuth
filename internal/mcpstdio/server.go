package mcpstdio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const protocolVersion = "2025-11-25"

type Server struct {
	baseURL             string
	middlewareToken     string
	defaultProjectID    string
	defaultProviderID   string
	defaultLLMProfileID string
	defaultLLMModel     string
	openAIProfileID     string
	openAIModel         string
	lmStudioBaseURL     string
	lmStudioAPIKey      string
	lmStudioProfileID   string
	lmStudioModel       string
	client              *http.Client
	logger              *log.Logger
}

type Options struct {
	BaseURL             string
	MiddlewareToken     string
	DefaultProjectID    string
	DefaultProviderID   string
	DefaultLLMProfileID string
	DefaultLLMModel     string
	OpenAIProfileID     string
	OpenAIModel         string
	LMStudioBaseURL     string
	LMStudioAPIKey      string
	LMStudioProfileID   string
	LMStudioModel       string
	HTTPClient          *http.Client
	Logger              *log.Logger
}

func New(options Options) *Server {
	if options.BaseURL == "" {
		options.BaseURL = "http://localhost:18787"
	}
	if options.DefaultProviderID == "" {
		options.DefaultProviderID = "openai"
	}
	options.BaseURL = strings.TrimRight(options.BaseURL, "/")
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	clientCopy := *options.HTTPClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if options.Logger == nil {
		options.Logger = log.New(io.Discard, "", 0)
	}
	return &Server{
		baseURL:             options.BaseURL,
		middlewareToken:     strings.TrimSpace(options.MiddlewareToken),
		defaultProjectID:    options.DefaultProjectID,
		defaultProviderID:   normalizeProviderID(options.DefaultProviderID),
		defaultLLMProfileID: options.DefaultLLMProfileID,
		defaultLLMModel:     options.DefaultLLMModel,
		openAIProfileID:     options.OpenAIProfileID,
		openAIModel:         options.OpenAIModel,
		lmStudioBaseURL:     options.LMStudioBaseURL,
		lmStudioAPIKey:      options.LMStudioAPIKey,
		lmStudioProfileID:   options.LMStudioProfileID,
		lmStudioModel:       options.LMStudioModel,
		client:              &clientCopy,
		logger:              options.Logger,
	}
}

func NewFromEnv(stderr io.Writer) *Server {
	return New(Options{
		BaseURL:             getenv("MIDDLEWARE_BASE_URL", "http://localhost:18787"),
		MiddlewareToken:     os.Getenv("MIDDLEWARE_CLIENT_TOKEN"),
		DefaultProjectID:    getenv("MCP_DEFAULT_PROJECT_ID", ""),
		DefaultProviderID:   getenv("MIDDLEWARE_LLM_PROVIDER", "openai"),
		DefaultLLMProfileID: os.Getenv("MIDDLEWARE_LLM_PROFILE_ID"),
		DefaultLLMModel:     os.Getenv("MIDDLEWARE_LLM_MODEL"),
		OpenAIProfileID:     os.Getenv("MCP_OPENAI_PROFILE_ID"),
		OpenAIModel:         os.Getenv("MCP_OPENAI_MODEL"),
		LMStudioBaseURL:     os.Getenv("LMSTUDIO_BASE_URL"),
		LMStudioAPIKey:      os.Getenv("LMSTUDIO_API_KEY"),
		LMStudioProfileID:   os.Getenv("MCP_LMSTUDIO_PROFILE_ID"),
		LMStudioModel:       os.Getenv("MCP_LMSTUDIO_MODEL"),
		Logger:              log.New(stderr, "middleware-auth-mcp ", log.LstdFlags),
	})
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if err := s.validateConfig(true); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeRPCError(writer, json.RawMessage("null"), -32700, "Parse error", nil)
			continue
		}
		if err := s.handle(ctx, writer, req); err != nil {
			s.logger.Printf("request failed: %v", err)
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, out *bufio.Writer, req rpcRequest) error {
	if req.JSONRPC != "2.0" {
		return writeRPCError(out, req.ID, -32600, "Invalid Request", nil)
	}
	switch req.Method {
	case "initialize":
		return writeRPCResult(out, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":        "middleware-openai-codex-oauth",
				"title":       "Middleware OpenAI Codex OAuth",
				"version":     "0.1.0",
				"description": "MCP wrapper para a API local do middlewareAuth",
			},
			"instructions": "Use llm_* para clientes novos. As tools openai_* e codex_responses continuam disponiveis por compatibilidade.",
		})
	case "notifications/initialized":
		return nil
	case "ping":
		return writeRPCResult(out, req.ID, map[string]any{})
	case "tools/list":
		return writeRPCResult(out, req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		return s.handleToolCall(ctx, out, req)
	default:
		if len(req.ID) == 0 {
			return nil
		}
		return writeRPCError(out, req.ID, -32601, "Method not found", nil)
	}
}

func (s *Server) handleToolCall(ctx context.Context, out *bufio.Writer, req rpcRequest) error {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return writeRPCError(out, req.ID, -32602, "Invalid params", nil)
	}
	text, isErr := s.callTool(ctx, params.Name, params.Arguments)
	return writeRPCResult(out, req.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": isErr,
	})
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (string, bool) {
	switch name {
	case "middleware_health":
		return s.middlewareHealth(ctx)
	case "llm_providers":
		return s.llmProviders(ctx, args)
	case "llm_login_start":
		return s.llmLoginStart(ctx, args)
	case "llm_login_status":
		return s.llmLoginStatus(ctx, args)
	case "llm_status":
		return s.llmStatus(ctx, args)
	case "llm_refresh":
		return s.llmRefresh(ctx, args)
	case "llm_responses":
		return s.llmResponses(ctx, args)
	case "openai_login_start":
		return s.openaiLoginStart(ctx, args)
	case "openai_status":
		return s.openaiStatus(ctx, args)
	case "openai_login_status":
		return s.openaiLoginStatus(ctx, args)
	case "openai_refresh":
		return s.openaiRefresh(ctx, args)
	case "codex_responses":
		return s.codexResponses(ctx, args)
	default:
		return "tool desconhecida: " + name, true
	}
}

func (s *Server) middlewareHealth(ctx context.Context) (string, bool) {
	var response any
	if err := s.doJSON(ctx, http.MethodGet, "/healthz", nil, &response, false); err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmProviders(ctx context.Context, args map[string]any) (string, bool) {
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", "", "", ""), true
	}
	var response any
	if err := s.doJSON(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(projectID)+"/llm/providers", nil, &response, true); err != nil {
		return s.llmErrorFromError(err, "", projectID, ""), true
	}
	return marshalText(response), false
}

func (s *Server) openaiLoginStart(ctx context.Context, args map[string]any) (string, bool) {
	response, err := s.openaiLoginStartValue(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmLoginStart(ctx context.Context, args map[string]any) (string, bool) {
	providerID := s.providerID(args)
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", providerID, "", s.profileIDForProvider(args, providerID)), true
	}
	profileID := s.profileIDForProvider(args, providerID)
	mode := stringArg(args, "mode", "")
	if mode == "" {
		if providerID == "lmstudio" {
			mode = "api_key"
		} else {
			mode = "device_code"
		}
	}
	body := map[string]any{
		"providerId": providerID,
		"profileId":  profileID,
		"mode":       mode,
	}
	authFields := objectArg(args, "authFields")
	if len(authFields) == 0 && providerID == "lmstudio" {
		authFields = map[string]any{
			"baseUrl": stringArg(args, "baseUrl", s.lmStudioBaseURL),
			"apiKey":  stringArg(args, "apiKey", s.lmStudioAPIKey),
		}
	}
	if len(authFields) > 0 {
		body["authFields"] = authFields
	}
	var response any
	if err := s.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/llm/login", body, &response, true); err != nil {
		return s.llmErrorFromError(err, providerID, projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) openaiLoginStartValue(ctx context.Context, args map[string]any) (any, error) {
	projectID, ok := s.projectID(args)
	if !ok {
		return nil, fmt.Errorf("projectId obrigatorio")
	}
	body := map[string]any{
		"profileId": stringArg(args, "profileId", s.profileIDForProvider(args, "openai")),
		"mode":      stringArg(args, "mode", "oauth"),
	}
	var response any
	if err := s.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/auth/openai/login", body, &response, true); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) openaiStatus(ctx context.Context, args map[string]any) (string, bool) {
	response, err := s.openaiStatusValue(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmStatus(ctx context.Context, args map[string]any) (string, bool) {
	providerID := s.providerID(args)
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", providerID, "", s.profileIDForProvider(args, providerID)), true
	}
	profileID := s.profileIDForProvider(args, providerID)
	path := "/v1/projects/" + url.PathEscape(projectID) + "/llm/status?providerId=" + url.QueryEscape(providerID) + "&profileId=" + url.QueryEscape(profileID)
	var response any
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return s.llmErrorFromError(err, providerID, projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) openaiStatusValue(ctx context.Context, args map[string]any) (map[string]any, error) {
	projectID, ok := s.projectID(args)
	if !ok {
		return nil, fmt.Errorf("projectId obrigatorio")
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/auth/openai/status?profileId=" + url.QueryEscape(stringArg(args, "profileId", s.profileIDForProvider(args, "openai")))
	var response map[string]any
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) openaiLoginStatus(ctx context.Context, args map[string]any) (string, bool) {
	response, err := s.openaiLoginStatusValue(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmLoginStatus(ctx context.Context, args map[string]any) (string, bool) {
	providerID := s.providerID(args)
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", providerID, "", s.profileIDForProvider(args, providerID)), true
	}
	sessionID := stringArg(args, "loginSessionId", "")
	if sessionID == "" {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "loginSessionId obrigatorio", providerID, projectID, s.profileIDForProvider(args, providerID)), true
	}
	profileID := s.profileIDForProvider(args, providerID)
	path := "/v1/projects/" + url.PathEscape(projectID) + "/llm/login-sessions/" + url.PathEscape(sessionID) + "?providerId=" + url.QueryEscape(providerID) + "&profileId=" + url.QueryEscape(profileID)
	var response any
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return s.llmErrorFromError(err, providerID, projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) openaiLoginStatusValue(ctx context.Context, args map[string]any) (map[string]any, error) {
	projectID, ok := s.projectID(args)
	if !ok {
		return nil, fmt.Errorf("projectId obrigatorio")
	}
	sessionID := stringArg(args, "loginSessionId", "")
	if sessionID == "" {
		return nil, fmt.Errorf("loginSessionId obrigatorio")
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/auth/openai/login-sessions/" + url.PathEscape(sessionID)
	var response map[string]any
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) openaiRefresh(ctx context.Context, args map[string]any) (string, bool) {
	response, err := s.openaiRefreshValue(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmRefresh(ctx context.Context, args map[string]any) (string, bool) {
	providerID := s.providerID(args)
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", providerID, "", s.profileIDForProvider(args, providerID)), true
	}
	profileID := s.profileIDForProvider(args, providerID)
	body := map[string]any{"providerId": providerID, "profileId": profileID}
	var response any
	if err := s.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/llm/refresh", body, &response, true); err != nil {
		return s.llmErrorFromError(err, providerID, projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) openaiRefreshValue(ctx context.Context, args map[string]any) (map[string]any, error) {
	projectID, ok := s.projectID(args)
	if !ok {
		return nil, fmt.Errorf("projectId obrigatorio")
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/auth/openai/refresh?profileId=" + url.QueryEscape(stringArg(args, "profileId", s.profileIDForProvider(args, "openai")))
	var response map[string]any
	if err := s.doJSON(ctx, http.MethodPost, path, nil, &response, true); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) codexResponses(ctx context.Context, args map[string]any) (string, bool) {
	response, err := s.codexResponsesValue(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	return marshalText(response), false
}

func (s *Server) llmResponses(ctx context.Context, args map[string]any) (string, bool) {
	providerID := s.providerID(args)
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", providerID, "", s.profileIDForProvider(args, providerID)), true
	}
	profileID := s.profileIDForProvider(args, providerID)
	input, err := parseInput(args["input"])
	if err != nil {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", err.Error(), providerID, projectID, profileID), true
	}
	body := map[string]any{
		"providerId":   providerID,
		"profileId":    profileID,
		"model":        s.modelForProvider(args, providerID),
		"instructions": stringArg(args, "instructions", ""),
		"input":        input,
		"stream":       boolArg(args, "stream", true),
		"store":        boolArg(args, "store", false),
	}
	if intelligence := stringArg(args, "intelligence", ""); intelligence != "" {
		body["intelligence"] = normalizeIntelligence(intelligence)
	}
	if reasoning := reasoningArg(args); len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}
	if extra := objectArg(args, "extra"); len(extra) > 0 {
		for key, value := range extra {
			if _, exists := body[key]; exists {
				continue
			}
			body[key] = value
		}
	}
	var response any
	if err := s.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/llm/responses", body, &response, true); err != nil {
		return s.llmErrorFromError(err, providerID, projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) codexResponsesValue(ctx context.Context, args map[string]any) (any, error) {
	projectID, ok := s.projectID(args)
	if !ok {
		return nil, fmt.Errorf("projectId obrigatorio")
	}
	input, err := parseInput(args["input"])
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":        stringArg(args, "model", s.modelForProvider(args, "openai")),
		"instructions": stringArg(args, "instructions", ""),
		"input":        input,
		"stream":       boolArg(args, "stream", true),
		"store":        boolArg(args, "store", false),
	}
	if intelligence := stringArg(args, "intelligence", ""); intelligence != "" {
		body["intelligence"] = normalizeIntelligence(intelligence)
	}
	if reasoning := reasoningArg(args); len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}
	if extra := objectArg(args, "extra"); len(extra) > 0 {
		for key, value := range extra {
			if _, exists := body[key]; exists {
				continue
			}
			body[key] = value
		}
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/codex/responses?profileId=" + url.QueryEscape(stringArg(args, "profileId", s.profileIDForProvider(args, "openai")))
	var response any
	if err := s.doJSON(ctx, http.MethodPost, path, body, &response, true); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) lmStudioLoginStart(ctx context.Context, args map[string]any) (string, bool) {
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", "lmstudio", "", s.profileIDForProvider(args, "lmstudio")), true
	}
	profileID := s.profileIDForProvider(args, "lmstudio")
	body := map[string]any{
		"profileId": profileID,
		"baseUrl":   stringArg(args, "baseUrl", s.lmStudioBaseURL),
		"apiKey":    stringArg(args, "apiKey", s.lmStudioAPIKey),
	}
	var response map[string]any
	if err := s.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/auth/lmstudio/api-key", body, &response, true); err != nil {
		return s.llmErrorFromError(err, "lmstudio", projectID, profileID), true
	}
	response["loginSessionId"] = "lmstudio-api-key-" + profileID
	response["status"] = "authenticated"
	return marshalText(response), false
}

func (s *Server) lmStudioStatus(ctx context.Context, args map[string]any) (string, bool) {
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", "lmstudio", "", s.profileIDForProvider(args, "lmstudio")), true
	}
	profileID := s.profileIDForProvider(args, "lmstudio")
	path := "/v1/projects/" + url.PathEscape(projectID) + "/auth/lmstudio/status?profileId=" + url.QueryEscape(profileID)
	var response map[string]any
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return s.llmErrorFromError(err, "lmstudio", projectID, profileID), true
	}
	return marshalText(llmStatusResponse("lmstudio", projectID, profileID, response)), false
}

func (s *Server) lmStudioResponses(ctx context.Context, args map[string]any) (string, bool) {
	projectID, ok := s.projectID(args)
	if !ok {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", "projectId obrigatorio", "lmstudio", "", s.profileIDForProvider(args, "lmstudio")), true
	}
	profileID := s.profileIDForProvider(args, "lmstudio")
	openArgs := cloneArgs(args)
	openArgs["model"] = s.modelForProvider(args, "lmstudio")
	body, err := lmStudioRequestBody(openArgs)
	if err != nil {
		return s.llmErrorText("ERR_LLM_REQUEST_INVALID", err.Error(), "lmstudio", projectID, profileID), true
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/lmstudio/responses?profileId=" + url.QueryEscape(profileID)
	var response any
	if err := s.doJSON(ctx, http.MethodPost, path, body, &response, true); err != nil {
		return s.llmErrorFromError(err, "lmstudio", projectID, profileID), true
	}
	return marshalText(response), false
}

func (s *Server) doJSON(ctx context.Context, method, path string, body any, out any, authRequired bool) error {
	if err := s.validateConfig(authRequired); err != nil {
		return err
	}
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authRequired {
		req.Header.Set("Authorization", "Bearer "+s.middlewareToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return fmt.Errorf("falha ao ler resposta do middleware")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("middleware HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(rawResp, out)
}

func (s *Server) validateConfig(requireToken bool) error {
	parsed, err := url.Parse(s.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return fmt.Errorf("MIDDLEWARE_BASE_URL invalida")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("MIDDLEWARE_BASE_URL precisa usar http ou https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("MIDDLEWARE_BASE_URL nao pode conter userinfo, query ou fragmento")
	}
	host := strings.ToLower(parsed.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("MIDDLEWARE_BASE_URL precisa apontar para um middleware local")
	}
	if requireToken && len(s.middlewareToken) < 32 {
		return fmt.Errorf("MIDDLEWARE_CLIENT_TOKEN precisa ter 32+ caracteres no ambiente do MCP")
	}
	return nil
}

func (s *Server) projectID(args map[string]any) (string, bool) {
	projectID := stringArg(args, "projectId", s.defaultProjectID)
	return projectID, projectID != ""
}

func (s *Server) providerID(args map[string]any) string {
	providerID := stringArg(args, "providerId", s.defaultProviderID)
	if providerID == "" {
		providerID = "openai"
	}
	return normalizeProviderID(providerID)
}

func (s *Server) profileIDForProvider(args map[string]any, providerID string) string {
	if profileID := stringArg(args, "profileId", ""); profileID != "" {
		return profileID
	}
	if s.defaultLLMProfileID != "" {
		return s.defaultLLMProfileID
	}
	if providerID == "openai" && s.openAIProfileID != "" {
		return s.openAIProfileID
	}
	if providerID == "lmstudio" && s.lmStudioProfileID != "" {
		return s.lmStudioProfileID
	}
	return "default"
}

func (s *Server) modelForProvider(args map[string]any, providerID string) string {
	if model := stringArg(args, "model", ""); model != "" {
		return model
	}
	if s.defaultLLMModel != "" {
		return s.defaultLLMModel
	}
	if providerID == "openai" && s.openAIModel != "" {
		return s.openAIModel
	}
	if providerID == "lmstudio" && s.lmStudioModel != "" {
		return s.lmStudioModel
	}
	if providerID == "lmstudio" {
		return "local-model"
	}
	return "gpt-5.5"
}

func normalizeProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func cloneArgs(args map[string]any) map[string]any {
	clone := make(map[string]any, len(args)+4)
	for key, value := range args {
		clone[key] = value
	}
	return clone
}

func llmStatusResponse(providerID, projectID, profileID string, response map[string]any) map[string]any {
	out := map[string]any{
		"authenticated": boolFromMap(response, "authenticated"),
		"providerId":    providerID,
		"projectId":     stringFromMapOr(response, "projectId", projectID),
		"profileId":     stringFromMapOr(response, "profileId", profileID),
	}
	copyOptional(out, response, "accountId")
	copyOptional(out, response, "email")
	copyOptional(out, response, "baseUrl")
	copyEpochOptional(out, response, "expires")
	if planType := stringFromMap(response, "planType"); planType != "" {
		out["planType"] = planType
	} else if planType := stringFromMap(response, "chatgptPlanType"); planType != "" {
		out["planType"] = planType
	}
	if metadata, ok := response["metadata"]; ok && metadata != nil {
		out["metadata"] = metadata
	}
	return out
}

func llmLoginStatusResponse(providerID, projectID, profileID string, response map[string]any) map[string]any {
	status := mapLoginStatus(stringFromMap(response, "status"))
	out := map[string]any{
		"providerId":     providerID,
		"projectId":      stringFromMapOr(response, "projectId", projectID),
		"profileId":      stringFromMapOr(response, "profileId", profileID),
		"loginSessionId": stringFromMap(response, "loginSessionId"),
		"status":         status,
		"authenticated":  status == "authenticated",
	}
	copyOptional(out, response, "mode")
	copyEpochOptional(out, response, "expiresAt")
	copyEpochOptional(out, response, "completedAt")
	copyOptional(out, response, "error")
	return out
}

func llmLoginStartResponse(response any) any {
	values, ok := response.(map[string]any)
	if !ok {
		return response
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	if value, ok := out["expiresAt"]; ok {
		out["expiresAt"] = epochSeconds(value)
	}
	return out
}

func mapLoginStatus(status string) string {
	switch status {
	case "completed":
		return "authenticated"
	case "pending", "authenticated", "expired", "failed":
		return status
	case "":
		return "pending"
	default:
		return status
	}
}

func copyOptional(out map[string]any, in map[string]any, key string) {
	if value, ok := in[key]; ok && value != nil {
		out[key] = value
	}
}

func copyEpochOptional(out map[string]any, in map[string]any, key string) {
	if value, ok := in[key]; ok && value != nil {
		out[key] = epochSeconds(value)
	}
}

func epochSeconds(value any) any {
	switch typed := value.(type) {
	case int64:
		if typed > 100000000000 {
			return typed / 1000
		}
		return typed
	case int:
		asInt64 := int64(typed)
		if asInt64 > 100000000000 {
			return asInt64 / 1000
		}
		return typed
	case float64:
		if typed > 100000000000 {
			return int64(typed / 1000)
		}
		if typed == float64(int64(typed)) {
			return int64(typed)
		}
		return typed
	default:
		return value
	}
}

func boolFromMap(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok || value == nil {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func stringFromMap(values map[string]any, key string) string {
	return stringFromMapOr(values, key, "")
}

func stringFromMapOr(values map[string]any, key string, fallback string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return fallback
	}
	return text
}

func (s *Server) llmErrorFromError(err error, providerID, projectID, profileID string) string {
	if err == nil {
		return s.llmErrorText("ERR_LLM_INTERNAL", "erro interno", providerID, projectID, profileID)
	}
	message := err.Error()
	code := "ERR_LLM_INTERNAL"
	publicMessage := "erro interno"
	switch {
	case strings.Contains(message, "ERR_LLM_PROVIDER_UNKNOWN"):
		code = "ERR_LLM_PROVIDER_UNKNOWN"
		publicMessage = "provider LLM desconhecido"
	case strings.Contains(message, "ERR_LLM_REFRESH_UNSUPPORTED"):
		code = "ERR_LLM_REFRESH_UNSUPPORTED"
		publicMessage = "provider nao suporta refresh"
	case strings.Contains(message, "ERR_LLM_AUTH_REQUIRED"):
		code = "ERR_LLM_AUTH_REQUIRED"
		publicMessage = "login necessario"
	case strings.Contains(message, "ERR_LLM_AUTH_EXPIRED"):
		code = "ERR_LLM_AUTH_EXPIRED"
		publicMessage = "credencial expirada ou invalida"
	case strings.Contains(message, "ERR_LLM_RATE_LIMITED"):
		code = "ERR_LLM_RATE_LIMITED"
		publicMessage = "rate limit do provider"
	case strings.Contains(message, "ERR_LLM_REQUEST_INVALID"):
		code = "ERR_LLM_REQUEST_INVALID"
		publicMessage = "request LLM invalido"
	case strings.Contains(message, "ERR_LLM_PROVIDER_UNAVAILABLE"):
		code = "ERR_LLM_PROVIDER_UNAVAILABLE"
		publicMessage = "provider indisponivel"
	case strings.Contains(message, "MIDDLEWARE_CLIENT_TOKEN") ||
		strings.Contains(message, "ERR_MIDDLEWARE_UNAUTHORIZED") ||
		strings.Contains(message, "ERR_AUTH_PROFILE_NOT_FOUND") ||
		strings.Contains(message, "ERR_LMSTUDIO_API_KEY_REQUIRED"):
		code = "ERR_LLM_AUTH_REQUIRED"
		publicMessage = "login necessario"
	case strings.Contains(message, "ERR_TOKEN_REFRESH_FAILED") ||
		strings.Contains(message, "ERR_ACCOUNT_ID_CHANGED") ||
		strings.Contains(message, "HTTP 401"):
		code = "ERR_LLM_AUTH_EXPIRED"
		publicMessage = "credencial expirada ou invalida"
	case strings.Contains(message, "ERR_CODEX_RATE_LIMITED") ||
		strings.Contains(message, "HTTP 429"):
		code = "ERR_LLM_RATE_LIMITED"
		publicMessage = "rate limit do provider"
	case strings.Contains(message, "ERR_CODEX_REQUEST_INVALID") ||
		strings.Contains(message, "ERR_LMSTUDIO_REQUEST_INVALID") ||
		strings.Contains(message, "ERR_LMSTUDIO_BASE_URL_INVALID") ||
		strings.Contains(message, "ERR_INVALID") ||
		strings.Contains(message, "HTTP 400"):
		code = "ERR_LLM_REQUEST_INVALID"
		publicMessage = "request LLM invalido"
	case strings.Contains(message, "ERR_CODEX_STREAM_INVALID") ||
		strings.Contains(message, "ERR_CODEX_HTTP_FAILED") ||
		strings.Contains(message, "ERR_LMSTUDIO_HTTP_FAILED") ||
		strings.Contains(message, "ERR_LMSTUDIO_RESPONSE_INVALID") ||
		strings.Contains(message, "HTTP 502") ||
		strings.Contains(message, "HTTP 503") ||
		strings.Contains(message, "HTTP 504"):
		code = "ERR_LLM_PROVIDER_UNAVAILABLE"
		publicMessage = "provider indisponivel"
	}
	return s.llmErrorText(code, publicMessage, providerID, projectID, profileID)
}

func (s *Server) llmErrorText(code, message, providerID, projectID, profileID string) string {
	errorBody := map[string]any{
		"code":       code,
		"message":    message,
		"providerId": providerID,
	}
	if projectID != "" {
		errorBody["projectId"] = projectID
	}
	if profileID != "" {
		errorBody["profileId"] = profileID
	}
	return marshalText(map[string]any{"error": errorBody})
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func writeRPCResult(out *bufio.Writer, id json.RawMessage, result any) error {
	if len(id) == 0 {
		return nil
	}
	return writeRPC(out, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(out *bufio.Writer, id json.RawMessage, code int, message string, data any) error {
	if len(id) == 0 {
		return nil
	}
	return writeRPC(out, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func writeRPC(out *bufio.Writer, response rpcResponse) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := out.Write(raw); err != nil {
		return err
	}
	if err := out.WriteByte('\n'); err != nil {
		return err
	}
	return out.Flush()
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "middleware_health",
			"title":       "Middleware Health",
			"description": "Consulta GET /healthz no middleware local.",
			"inputSchema": objectSchema(nil, nil),
		},
		{
			"name":        "llm_providers",
			"title":       "LLM Providers",
			"description": "Lista providers LLM suportados e capacidades.",
			"inputSchema": objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"},
			}, []string{"projectId"}),
		},
		{
			"name":        "llm_login_start",
			"title":       "Start LLM Login",
			"description": "Inicia login OAuth ou device-code para um provider/projeto/perfil.",
			"inputSchema": objectSchema(map[string]any{
				"providerId": map[string]any{"type": "string", "default": "openai"},
				"projectId":  map[string]any{"type": "string"},
				"profileId":  map[string]any{"type": "string", "default": "default"},
				"mode":       map[string]any{"type": "string", "description": "Modo retornado por llm_providers.auth.modes."},
				"authFields": map[string]any{"type": "object", "description": "Valores dos campos retornados por llm_providers.auth.fields.", "additionalProperties": true},
			}, []string{"providerId", "projectId", "profileId"}),
		},
		{
			"name":        "llm_login_status",
			"title":       "LLM Login Status",
			"description": "Consulta status normalizado de uma sessao de login de provider LLM.",
			"inputSchema": objectSchema(map[string]any{
				"providerId":     map[string]any{"type": "string", "default": "openai"},
				"projectId":      map[string]any{"type": "string"},
				"loginSessionId": map[string]any{"type": "string"},
				"profileId":      map[string]any{"type": "string"},
			}, []string{"providerId", "projectId", "loginSessionId"}),
		},
		{
			"name":        "llm_status",
			"title":       "LLM Auth Status",
			"description": "Consulta credencial normalizada de um provider LLM.",
			"inputSchema": objectSchema(map[string]any{
				"providerId": map[string]any{"type": "string", "default": "openai"},
				"projectId":  map[string]any{"type": "string"},
				"profileId":  map[string]any{"type": "string", "default": "default"},
			}, []string{"providerId", "projectId", "profileId"}),
		},
		{
			"name":        "llm_refresh",
			"title":       "Refresh LLM Token",
			"description": "Forca refresh da credencial do provider LLM quando suportado.",
			"inputSchema": objectSchema(map[string]any{
				"providerId": map[string]any{"type": "string", "default": "openai"},
				"projectId":  map[string]any{"type": "string"},
				"profileId":  map[string]any{"type": "string", "default": "default"},
			}, []string{"providerId", "projectId", "profileId"}),
		},
		{
			"name":        "llm_responses",
			"title":       "LLM Responses",
			"description": "Executa uma chamada LLM generica via provider configurado.",
			"inputSchema": objectSchema(map[string]any{
				"providerId":       map[string]any{"type": "string", "default": "openai"},
				"projectId":        map[string]any{"type": "string"},
				"profileId":        map[string]any{"type": "string", "default": "default"},
				"model":            map[string]any{"type": "string", "default": "gpt-5.5", "description": "String livre provider-specific."},
				"intelligence":     map[string]any{"type": "string", "description": "Nivel livre do backend. Exemplos atuais: instant, thinking."},
				"instructions":     map[string]any{"type": "string"},
				"reasoningEffort":  map[string]any{"type": "string", "description": "Campo portavel; aliases: padrao -> medium, estendido -> high."},
				"reasoningSummary": map[string]any{"type": "string", "description": "Resumo de raciocinio quando suportado pelo backend."},
				"reasoning":        map[string]any{"type": "object", "description": "Objeto reasoning bruto; tem precedencia sobre reasoningEffort/reasoningSummary."},
				"extra":            map[string]any{"type": "object", "description": "Campos top-level futuros repassados ao backend sem override dos campos conhecidos."},
				"input": map[string]any{
					"oneOf": []map[string]any{
						{"type": "string"},
						{"type": "array", "items": objectSchema(map[string]any{
							"role":    map[string]any{"type": "string", "enum": []string{"user", "assistant", "system", "developer"}},
							"content": map[string]any{"type": "string"},
						}, []string{"role", "content"})},
					},
				},
				"stream": map[string]any{"type": "boolean", "default": true},
				"store":  map[string]any{"type": "boolean", "default": false},
			}, []string{"providerId", "projectId", "profileId", "model", "input"}),
		},
		{
			"name":        "openai_login_start",
			"title":       "Start OpenAI Login",
			"description": "Inicia login OAuth ou device-code para um projeto/perfil.",
			"inputSchema": objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string", "description": "Projeto do middleware."},
				"profileId": map[string]any{"type": "string", "description": "Perfil OAuth. Default: default."},
				"mode":      map[string]any{"type": "string", "enum": []string{"oauth", "device_code"}, "description": "Modo de login."},
			}, []string{"projectId"}),
		},
		{
			"name":        "openai_status",
			"title":       "OpenAI Auth Status",
			"description": "Consulta status de autenticacao de um projeto/perfil.",
			"inputSchema": objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"},
				"profileId": map[string]any{"type": "string"},
			}, []string{"projectId"}),
		},
		{
			"name":        "openai_login_status",
			"title":       "OpenAI Login Status",
			"description": "Consulta status operacional de uma sessao de login OAuth ou device-code.",
			"inputSchema": objectSchema(map[string]any{
				"projectId":      map[string]any{"type": "string"},
				"loginSessionId": map[string]any{"type": "string"},
			}, []string{"projectId", "loginSessionId"}),
		},
		{
			"name":        "openai_refresh",
			"title":       "Refresh OpenAI Token",
			"description": "Forca refresh controlado do token de um projeto/perfil.",
			"inputSchema": objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"},
				"profileId": map[string]any{"type": "string"},
			}, []string{"projectId"}),
		},
		{
			"name":        "codex_responses",
			"title":       "Codex Responses",
			"description": "Chama /codex/responses via middleware usando o perfil OAuth salvo.",
			"inputSchema": objectSchema(map[string]any{
				"projectId":        map[string]any{"type": "string"},
				"profileId":        map[string]any{"type": "string"},
				"model":            map[string]any{"type": "string", "default": "gpt-5.5"},
				"intelligence":     map[string]any{"type": "string", "description": "Nivel livre do backend. Exemplos atuais: instant, thinking. Nao e enum rigido."},
				"instructions":     map[string]any{"type": "string"},
				"reasoningEffort":  map[string]any{"type": "string", "description": "Esforco livre ou alias: padrao -> medium, estendido -> high."},
				"reasoningSummary": map[string]any{"type": "string", "description": "Resumo de raciocinio quando suportado pelo backend."},
				"reasoning":        map[string]any{"type": "object", "description": "Objeto reasoning bruto; tem precedencia sobre reasoningEffort/reasoningSummary."},
				"extra":            map[string]any{"type": "object", "description": "Campos top-level futuros repassados ao backend sem override dos campos conhecidos."},
				"input": map[string]any{
					"oneOf": []map[string]any{
						{"type": "string"},
						{"type": "array", "items": objectSchema(map[string]any{
							"role":    map[string]any{"type": "string", "enum": []string{"user", "assistant", "system", "developer"}},
							"content": map[string]any{"type": "string"},
						}, []string{"role", "content"})},
					},
				},
				"stream": map[string]any{"type": "boolean", "default": true},
				"store":  map[string]any{"type": "boolean", "default": false},
			}, []string{"projectId", "input"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if required != nil {
		schema["required"] = required
	}
	return schema
}

func parseInput(value any) ([]map[string]string, error) {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil, fmt.Errorf("input vazio")
		}
		return []map[string]string{{"role": "user", "content": typed}}, nil
	case []any:
		items := make([]map[string]string, 0, len(typed))
		for _, item := range typed {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("input array precisa conter objetos")
			}
			role := stringArg(obj, "role", "user")
			content := stringArg(obj, "content", "")
			if content == "" {
				return nil, fmt.Errorf("input.content obrigatorio")
			}
			items = append(items, map[string]string{"role": role, "content": content})
		}
		return items, nil
	default:
		return nil, fmt.Errorf("input obrigatorio")
	}
}

func lmStudioRequestBody(args map[string]any) (map[string]any, error) {
	input, err := parseInput(args["input"])
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":        stringArg(args, "model", "local-model"),
		"instructions": stringArg(args, "instructions", ""),
		"input":        input,
		"stream":       boolArg(args, "stream", false),
		"store":        false,
	}
	if extra := objectArg(args, "extra"); len(extra) > 0 {
		for key, value := range extra {
			if _, exists := body[key]; exists {
				continue
			}
			body[key] = value
		}
	}
	return body, nil
}

func stringArg(args map[string]any, key string, fallback string) string {
	if args == nil {
		return fallback
	}
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return fallback
	}
	return text
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	typed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return typed
}

func objectArg(args map[string]any, key string) map[string]any {
	if args == nil {
		return nil
	}
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func reasoningArg(args map[string]any) map[string]any {
	if raw := objectArg(args, "reasoning"); len(raw) > 0 {
		return raw
	}
	reasoning := make(map[string]any)
	if effort := stringArg(args, "reasoningEffort", ""); effort != "" {
		reasoning["effort"] = normalizeReasoningEffort(effort)
	} else if effort := stringArg(args, "effort", ""); effort != "" {
		reasoning["effort"] = normalizeReasoningEffort(effort)
	}
	if summary := stringArg(args, "reasoningSummary", ""); summary != "" {
		reasoning["summary"] = summary
	}
	return reasoning
}

func normalizeIntelligence(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeReasoningEffort(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer(
		"\u00e3", "a",
		"\u00e1", "a",
		"\u00e0", "a",
		"\u00e2", "a",
		"\u00e9", "e",
		"\u00ea", "e",
		"\u00ed", "i",
		"\u00f3", "o",
		"\u00f4", "o",
		"\u00fa", "u",
		"\u00e7", "c",
	).Replace(normalized)
	switch normalized {
	case "padrao", "default", "standard":
		return "medium"
	case "estendido":
		return "high"
	default:
		return normalized
	}
}

func marshalText(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
