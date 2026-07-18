package llmcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/irinery/middlewareAuth/internal/security"
)

const (
	MaxOutputContractIDBytes = 128
	MaxJSONSchemaBytes       = 64 << 10
	MaxJSONSchemaDepth       = 16
	MaxJSONSchemaProperties  = 256
)

var (
	outputContractIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	schemaHashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// OutputContract is the provider-agnostic structured-output request accepted
// by MiddlewareAuth. SchemaHash is governance metadata and is intentionally not
// forwarded to providers.
type OutputContract struct {
	ID         string          `json:"id"`
	SchemaHash string          `json:"schemaHash"`
	Strict     bool            `json:"strict"`
	JSONSchema json.RawMessage `json:"jsonSchema"`
}

func ValidateOutputContract(contract *OutputContract) error {
	if contract == nil {
		return nil
	}
	if contract.ID != strings.TrimSpace(contract.ID) || len(contract.ID) == 0 || len(contract.ID) > MaxOutputContractIDBytes || !outputContractIDPattern.MatchString(contract.ID) {
		return invalidOutputContract("outputContract.id", "identificador invalido")
	}
	if !schemaHashPattern.MatchString(contract.SchemaHash) {
		return invalidOutputContract("outputContract.schemaHash", "use sha256:<64 hex minusculos>")
	}
	if !contract.Strict {
		return invalidOutputContract("outputContract.strict", "precisa ser true")
	}
	if len(contract.JSONSchema) == 0 || len(contract.JSONSchema) > MaxJSONSchemaBytes {
		return invalidOutputContract("outputContract.jsonSchema", "objeto JSON de ate 64 KiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(contract.JSONSchema))
	decoder.UseNumber()
	var schema any
	if err := decoder.Decode(&schema); err != nil {
		return invalidOutputContract("outputContract.jsonSchema", "JSON invalido")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return invalidOutputContract("outputContract.jsonSchema", "JSON invalido")
	}
	if _, ok := schema.(map[string]any); !ok {
		return invalidOutputContract("outputContract.jsonSchema", "precisa ser um objeto JSON")
	}
	if depth, properties := schemaComplexity(schema, 1); depth > MaxJSONSchemaDepth {
		return invalidOutputContract("outputContract.jsonSchema", "profundidade maxima de 16")
	} else if properties > MaxJSONSchemaProperties {
		return invalidOutputContract("outputContract.jsonSchema", "ate 256 propriedades declaradas")
	}
	return nil
}

func UnsupportedOutputContract() error {
	return security.NewError("ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED", "provider ou modelo nao suporta outputContract", http.StatusUnprocessableEntity)
}

// ProviderSchemaName converts the stable contract ID to the conservative name
// syntax shared by the OpenAI-compatible wires. A hash suffix avoids collisions
// when normalization or truncation is needed.
func ProviderSchemaName(contract *OutputContract) string {
	if contract == nil {
		return ""
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, contract.ID)
	if name == contract.ID && len(name) <= 64 {
		return name
	}
	hash := strings.TrimPrefix(contract.SchemaHash, "sha256:")
	if len(hash) > 16 {
		hash = hash[:16]
	}
	if len(name) > 47 {
		name = name[:47]
	}
	return strings.TrimRight(name, "_") + "_" + hash
}

// ProviderJSONSchema creates a transport copy and removes the root $schema
// dialect annotation, which is not part of the Structured Outputs subset. The
// public JSONSchema and SchemaHash remain untouched.
func ProviderJSONSchema(contract *OutputContract) (json.RawMessage, error) {
	if contract == nil {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contract.JSONSchema, &fields); err != nil {
		return nil, invalidOutputContract("outputContract.jsonSchema", "JSON invalido")
	}
	delete(fields, "$schema")
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, invalidOutputContract("outputContract.jsonSchema", "JSON invalido")
	}
	return raw, nil
}

// NormalizeStructuredOutputText enforces only transport-level conformance. It
// does not validate the object against the domain schema; that remains the
// consumer's responsibility.
func NormalizeStructuredOutputText(outputText string) (string, error) {
	trimmed := strings.TrimSpace(outputText)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return "", UnsupportedOutputContract()
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
		return "", UnsupportedOutputContract()
	}
	return trimmed, nil
}

func invalidOutputContract(field, constraint string) error {
	err := security.NewError("ERR_LLM_REQUEST_INVALID", "outputContract invalido", http.StatusBadRequest)
	return security.WithDetail(err, field, constraint)
}

func schemaComplexity(value any, depth int) (int, int) {
	type node struct {
		value any
		depth int
	}
	stack := []node{{value: value, depth: depth}}
	maxDepth := 0
	properties := 0
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.depth > maxDepth {
			maxDepth = current.depth
		}
		if maxDepth > MaxJSONSchemaDepth || properties > MaxJSONSchemaProperties {
			return maxDepth, properties
		}
		switch typed := current.value.(type) {
		case map[string]any:
			if declared, ok := typed["properties"].(map[string]any); ok {
				properties += len(declared)
			}
			for _, child := range typed {
				stack = append(stack, node{value: child, depth: current.depth + 1})
			}
		case []any:
			for _, child := range typed {
				stack = append(stack, node{value: child, depth: current.depth + 1})
			}
		}
	}
	return maxDepth, properties
}
