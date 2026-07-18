package llmcontract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/irinery/middlewareAuth/internal/security"
)

const testSchemaHash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestValidateOutputContract(t *testing.T) {
	valid := func() *OutputContract {
		return &OutputContract{
			ID:         "pockettrace.AIValidatedEnrichment.v1",
			SchemaHash: testSchemaHash,
			Strict:     true,
			JSONSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`),
		}
	}
	if err := ValidateOutputContract(valid()); err != nil {
		t.Fatalf("valid contract: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*OutputContract)
	}{
		{name: "id", mutate: func(c *OutputContract) { c.ID = "invalid id" }},
		{name: "hash", mutate: func(c *OutputContract) { c.SchemaHash = "sha256:ABC" }},
		{name: "strict", mutate: func(c *OutputContract) { c.Strict = false }},
		{name: "schema primitive", mutate: func(c *OutputContract) { c.JSONSchema = json.RawMessage(`true`) }},
		{name: "schema trailing JSON", mutate: func(c *OutputContract) { c.JSONSchema = json.RawMessage(`{} {}`) }},
		{name: "schema bytes", mutate: func(c *OutputContract) {
			c.JSONSchema = json.RawMessage(`{"description":"` + strings.Repeat("a", MaxJSONSchemaBytes) + `"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := valid()
			test.mutate(contract)
			if err := ValidateOutputContract(contract); security.Code(err) != "ERR_LLM_REQUEST_INVALID" {
				t.Fatalf("error=%v code=%s", err, security.Code(err))
			}
		})
	}
}

func TestValidateOutputContractComplexityLimits(t *testing.T) {
	properties := make(map[string]any, MaxJSONSchemaProperties+1)
	for index := 0; index <= MaxJSONSchemaProperties; index++ {
		properties[string(rune('a'+index%26))+strings.Repeat("x", index/26)] = map[string]any{"type": "string"}
	}
	raw, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	if err != nil {
		t.Fatal(err)
	}
	contract := &OutputContract{ID: "schema.v1", SchemaHash: testSchemaHash, Strict: true, JSONSchema: raw}
	if err := ValidateOutputContract(contract); security.Code(err) != "ERR_LLM_REQUEST_INVALID" {
		t.Fatalf("properties error=%v code=%s", err, security.Code(err))
	}

	var nested any = map[string]any{"type": "string"}
	for index := 0; index < MaxJSONSchemaDepth; index++ {
		nested = map[string]any{"items": nested}
	}
	raw, err = json.Marshal(nested)
	if err != nil {
		t.Fatal(err)
	}
	contract.JSONSchema = raw
	if err := ValidateOutputContract(contract); security.Code(err) != "ERR_LLM_REQUEST_INVALID" {
		t.Fatalf("depth error=%v code=%s", err, security.Code(err))
	}
}

func TestProviderSchemaNameIsWireSafeAndStable(t *testing.T) {
	contract := &OutputContract{ID: "pockettrace.AIValidatedEnrichment.v1", SchemaHash: testSchemaHash}
	name := ProviderSchemaName(contract)
	if name != "pockettrace_AIValidatedEnrichment_v1_0123456789abcdef" || len(name) > 64 {
		t.Fatalf("name=%q", name)
	}
}

func TestProviderJSONSchemaPreservesPublicSchemaAndPocketTraceSubset(t *testing.T) {
	publicSchema := json.RawMessage(`{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"$defs":{
			"nullableSummary":{"anyOf":[{"type":"string","minLength":1,"maxLength":200},{"type":"null"}]},
			"evidence":{"type":"object","properties":{"kind":{"const":"source"},"items":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":8}},"required":["kind","items"],"additionalProperties":false}
		},
		"type":"object",
		"properties":{"summary":{"$ref":"#/$defs/nullableSummary"},"evidence":{"$ref":"#/$defs/evidence"}},
		"required":["summary","evidence"],
		"additionalProperties":false
	}`)
	contract := &OutputContract{ID: "pockettrace.AIValidatedEnrichment.v1", SchemaHash: testSchemaHash, Strict: true, JSONSchema: publicSchema}
	before := append([]byte(nil), contract.JSONSchema...)
	wireSchema, err := ProviderJSONSchema(contract)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(wireSchema) || strings.Contains(string(wireSchema), `"$schema"`) {
		t.Fatalf("wire schema=%s", wireSchema)
	}
	for _, keyword := range []string{`"$defs"`, `"$ref"`, `"anyOf"`, `"type":"null"`, `"const"`, `"minLength"`, `"maxLength"`, `"minItems"`, `"maxItems"`} {
		if !strings.Contains(string(wireSchema), keyword) {
			t.Fatalf("wire schema perdeu %s: %s", keyword, wireSchema)
		}
	}
	if string(contract.JSONSchema) != string(before) || contract.SchemaHash != testSchemaHash || !strings.Contains(string(contract.JSONSchema), `"$schema"`) {
		t.Fatalf("public contract was mutated: %#v", contract)
	}
}

func TestNormalizeStructuredOutputTextRejectsProseAndFences(t *testing.T) {
	got, err := NormalizeStructuredOutputText("  {\"summary\":\"ok\"}\n")
	if err != nil || got != `{"summary":"ok"}` {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, output := range []string{
		"```json\n{\"summary\":\"ok\"}\n```",
		"Resultado: {\"summary\":\"ok\"}",
		`["not", "an", "object"]`,
		"",
	} {
		if _, err := NormalizeStructuredOutputText(output); security.Code(err) != "ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED" {
			t.Fatalf("output=%q err=%v code=%s", output, err, security.Code(err))
		}
	}
}
