package llmcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	actualHash := SchemaHash(contract.JSONSchema)
	if contract.SchemaHash != actualHash {
		return invalidOutputContract("outputContract.schemaHash", "hash nao corresponde a jsonSchema")
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

// SchemaHash calculates the canonical contract digest used by consumers. The
// schema must already be serialized canonically; surrounding whitespace is
// ignored consistently with PocketKernel.
func SchemaHash(schema json.RawMessage) string {
	canonical := bytes.TrimSpace(schema)
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err == nil {
		if marshaled, err := json.Marshal(value); err == nil {
			canonical = marshaled
		}
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func UnsupportedOutputContract() *security.AppError {
	return security.NewError("ERR_LLM_OUTPUT_CONTRACT_UNSUPPORTED", "provider ou modelo nao suporta outputContract", http.StatusUnprocessableEntity)
}

func UnsupportedOutputContractWithReason(reason string) error {
	err := UnsupportedOutputContract()
	if !IsSafeOutputContractReason(reason) {
		return err
	}
	return security.WithDetail(err, "provider_reason", reason)
}

func IsSafeOutputContractReason(reason string) bool {
	switch reason {
	case "empty_output", "fenced_output", "invalid_json_output", "root_not_object",
		"text_format", "response_format", "json_schema", "schema", "max_output_tokens",
		"tools", "include", "provider_code", "request_rejected":
		return true
	default:
		return false
	}
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
	if trimmed == "" {
		return "", UnsupportedOutputContractWithReason("empty_output")
	}
	if strings.HasPrefix(trimmed, "```") {
		return "", UnsupportedOutputContractWithReason("fenced_output")
	}
	if !json.Valid([]byte(trimmed)) {
		return "", UnsupportedOutputContractWithReason("invalid_json_output")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
		return "", UnsupportedOutputContractWithReason("root_not_object")
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
