# Evidencia da issue #5: isolamento e falha nao bloqueante

Execucao E2E real: `2026-07-14T00:31:10Z`.

O runner `scripts/e2e-live-lmstudio.sh` compilou os dois entrypoints, iniciou o
middleware em diretorio temporario sem PocketTrace, PocketWiki ou outro
consumidor e chamou uma instancia real do LM Studio 0.4.19+2 autenticada. O
modelo real carregado foi `liquid/lfm2.5-1.2b`.

Os 24 checks E2E passaram:

```text
provider-real-authenticated
http-canonical-catalog
http-login
http-login-status
http-status
http-project-isolation
http-profile-isolation
middleware-authorization-401
unknown-provider-400
login-session-404
refresh-unsupported-400
http-responses
provider-auth-401
provider-failure-non-blocking
http-legacy-compatibility
mcp-protocol
mcp-canonical-catalog
mcp-login
mcp-login-status
mcp-status
mcp-responses
credential-redaction
encrypted-at-rest
server-log-redaction
```

A credencial invalida foi efetivamente recusada pelo LM Studio real. Depois da
falha, `healthz`, catalogo em outro projeto e uma nova inferencia autenticada
continuaram funcionando. A API key real e a credencial invalida nao apareceram
nas respostas HTTP/MCP, erros, logs ou store em texto claro.

Cobertura automatizada complementar:

- isolamento criptografico entre provider, projeto e profile;
- mapeamento de 401/403, 408/504, 429 e 5xx nos transports OpenAI/LM Studio;
- descarte do corpo de erro do provider, inclusive quando contem canary de
  token/API key;
- normalizacao HTTP para os codigos publicos recuperaveis `ERR_LLM_*`;
- timeout de rede real no transport, sem mock de interface;
- provider desconhecido e sessao de login inexistente.

Gates executados:

```text
sh -n scripts/e2e-live-lmstudio.sh
shellcheck scripts/e2e-live-lmstudio.sh
go test ./...
go test -race ./...
go vet ./...
go build ./...
./scripts/check-no-secrets.sh
git diff --check
```

Nenhum checkout, build, runtime ou arquivo de consumidor foi usado.
