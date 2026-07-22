package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const contractFixturePath = "../../docs/examples/llm-http-payloads.json"

func TestCanonicalContractFixtureMatchesRuntimeCatalog(t *testing.T) {
	raw := readContractFile(t, contractFixturePath)
	var fixture struct {
		ContractVersion string `json:"contractVersion"`
		Providers       struct {
			Response json.RawMessage `json:"response"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("fixture JSON invalido: %v", err)
	}
	if fixture.ContractVersion != "middlewareauth.llm.v1" {
		t.Fatalf("contractVersion = %q", fixture.ContractVersion)
	}

	handler := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/contract-test/llm/providers", nil)
	req.Header.Set("Authorization", "Bearer "+handlerTestToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", rec.Code, rec.Body.String())
	}

	var documented, runtime any
	if err := json.Unmarshal(fixture.Providers.Response, &documented); err != nil {
		t.Fatalf("catalogo documentado invalido: %v", err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runtime); err != nil {
		t.Fatalf("catalogo runtime invalido: %v", err)
	}
	if !reflect.DeepEqual(documented, runtime) {
		documentedJSON, _ := json.MarshalIndent(documented, "", "  ")
		runtimeJSON, _ := json.MarshalIndent(runtime, "", "  ")
		t.Fatalf("fixture diverge do runtime\ndocumentado=%s\nruntime=%s", documentedJSON, runtimeJSON)
	}
}

func TestCanonicalContractFixtureIsCompleteAndContainsNoRealSecret(t *testing.T) {
	raw := readContractFile(t, contractFixturePath)
	var fixture map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("fixture JSON invalido: %v", err)
	}
	for _, section := range []string{"providers", "login", "loginStatus", "status", "refresh", "responses", "errors", "mcp"} {
		if _, ok := fixture[section]; !ok {
			t.Errorf("secao obrigatoria ausente: %s", section)
		}
	}

	errors, ok := fixture["errors"].(map[string]any)
	if !ok || len(errors) != 10 {
		t.Fatalf("catalogo de erros incompleto: %#v", fixture["errors"])
	}
	mcp := fixture["mcp"].(map[string]any)
	tools := mcp["tools"].(map[string]any)
	for _, name := range []string{"llm_providers", "llm_login_start", "llm_login_status", "llm_status", "llm_refresh", "llm_responses"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("payload MCP ausente: %s", name)
		}
	}

	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9._~:+/=-]{8,}`),
		regexp.MustCompile(`Bearer\s+[A-Za-z0-9._~+/=-]{24,}`),
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	} {
		if match := pattern.Find(raw); match != nil {
			t.Fatalf("fixture contem possivel segredo real: %q", match)
		}
	}
	if count := strings.Count(string(raw), `"apiKey": "<secret>"`); count != 2 {
		t.Fatalf("apiKey deve aparecer apenas como placeholder nos payloads HTTP/MCP; ocorrencias=%d", count)
	}
	if strings.Contains(string(raw), `"accessToken"`) || strings.Contains(string(raw), `"refreshToken"`) {
		t.Fatal("fixture nao pode publicar accessToken/refreshToken")
	}
}

func TestCanonicalContractDocumentsIndependenceStabilityAndDeprecation(t *testing.T) {
	raw := readContractFile(t, "../../docs/LLM_PROVIDER_CONTRACT.md")
	text := string(raw)
	for _, required := range []string{
		"Cada projeto tem que funcionar sozinho",
		"Campos estaveis",
		"Valores provider-specific e metadata",
		"Politica de compatibilidade e depreciacao",
		"auth.fields",
		"capabilities",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("documento canonico nao contem %q", required)
		}
	}
}

func readContractFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("falha ao ler %s: %v", path, err)
	}
	return raw
}
