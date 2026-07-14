# Evidencia da issue #4: contrato canonico

Execucao: `2026-07-14T00:22:43Z`.

Artefatos auditados:

- `docs/LLM_PROVIDER_CONTRACT.md`: fonte de verdade HTTP/MCP;
- `docs/examples/llm-http-payloads.json`: payloads consumiveis por frontend;
- `AGENTS.md`: regra operacional "Cada projeto tem que funcionar sozinho";
- `internal/httpapi/contract_docs_test.go`: validacao automatica entre fixture e runtime.

Garantias verificadas automaticamente:

- o JSON de exemplos e sintaticamente valido;
- o catalogo documentado e estruturalmente igual ao catalogo HTTP real;
- todas as operacoes HTTP e as seis tools MCP possuem payload de exemplo;
- os nove codigos publicos `ERR_LLM_*` possuem envelope e status documentados;
- `apiKey` aparece somente como `<secret>`;
- nao existem access token, refresh token, token LM Studio ou JWT reais;
- o documento declara independencia, campos estaveis, valores
  provider-specific/metadata e politica de depreciacao;
- `intelligence` e `store` sao dirigidos por capabilities, sem condicional por
  nome do provider.

Gates executados:

```text
jq empty docs/examples/llm-http-payloads.json
go test -count=1 ./internal/httpapi
go test ./...
go vet ./...
./scripts/check-no-secrets.sh
git diff --check
```

O E2E real foi repetido contra LM Studio autenticado e passou os 16 checks. Ele
confirmou que HTTP e MCP retornam o mesmo catalogo atualizado e que login,
status, inferencia, compatibilidade, isolamento, redaction e criptografia em
repouso continuam funcionais.
