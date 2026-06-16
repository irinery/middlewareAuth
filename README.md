# middlewareAuth

Middleware Go para centralizar autenticacao OpenAI/Codex via OAuth, persistir perfis por projeto e expor uma API interna para outros softwares consumirem sem lidar com refresh token.

O desenho segue os contratos em `/Users/irinery/Downloads/middleware`, inspirado conceitualmente no OpenClaw, mas sem runtime Node/TypeScript.

## Estado atual

Implementado:

- config runtime com allowlist, timeouts e state dir seguro;
- OAuth PKCE e device-code;
- store criptografado AES-256-GCM com escrita atomica;
- identity JWT e refresh com lock por perfil;
- transporte Codex HTTP/SSE com headers protegidos e retry;
- API HTTP interna;
- SDK Go em `pkg/client`;
- auditoria/log redigido e healthcheck.

## Config minima

```sh
export MIDDLEWARE_SECRET_KEY="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_CLIENT_TOKEN="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_STATE_DIR='/Users/irinery/Documents/middlewareAuth/.middleware-state'
export HTTP_BIND_ADDR='127.0.0.1'
export HTTP_PORT=18787
```

Nao use `.env`: arquivos `.env*` sao bloqueados no repo. Injete essas variaveis pelo shell, launchd, systemd, Docker/Kubernetes secret ou pelo cliente MCP.

`MIDDLEWARE_SECRET_KEY` com 32+ caracteres e `MIDDLEWARE_CLIENT_TOKEN` sao obrigatorios fora de `NODE_ENV=test`.
Em dev, prefira `MIDDLEWARE_STATE_DIR` absoluto para nao criar stores diferentes conforme o diretorio de execucao.
Por default o servidor escuta so em `127.0.0.1`; para expor em rede, configure `HTTP_BIND_ADDR` e `MIDDLEWARE_ALLOW_NON_LOOPBACK_BIND=true` de forma explicita.

## Rodar

```sh
go run ./cmd/middleware-codex-oauth
```

Mais exemplos de acesso: [docs/ACCESS.md](/Users/irinery/Documents/middlewareAuth/docs/ACCESS.md).

Endpoints principais:

```text
GET  http://localhost:18787/healthz
POST http://localhost:18787/v1/projects/{projectId}/auth/openai/login
GET  http://localhost:18787/v1/auth/openai/callback
GET  http://localhost:18787/v1/projects/{projectId}/auth/openai/login-sessions/{loginSessionId}
GET  http://localhost:18787/v1/projects/{projectId}/auth/openai/status
POST http://localhost:18787/v1/projects/{projectId}/auth/openai/refresh
POST http://localhost:18787/v1/projects/{projectId}/codex/responses
POST http://localhost:18787/v1/projects/{projectId}/auth/lmstudio/api-key
GET  http://localhost:18787/v1/projects/{projectId}/auth/lmstudio/status
POST http://localhost:18787/v1/projects/{projectId}/lmstudio/responses
```

Nos endpoints protegidos:

```http
Authorization: Bearer <MIDDLEWARE_CLIENT_TOKEN>
```

Exemplo local:

```sh
curl -s http://localhost:18787/healthz

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  http://localhost:18787/v1/projects/acme/auth/openai/status

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"profileId":"default","mode":"oauth"}' \
  http://localhost:18787/v1/projects/acme/auth/openai/login

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  http://localhost:18787/v1/projects/acme/auth/openai/login-sessions/<loginSessionId>
```

## Verificacao

```sh
sh ./scripts/check-no-secrets.sh
go test ./...
go test -race ./...
go build ./cmd/middleware-codex-oauth
go build ./cmd/middleware-codex-oauth-mcp
```

## MCP

Servidor MCP local por stdio:

```sh
go run ./cmd/middleware-codex-oauth-mcp
```

Detalhes e config de cliente: [docs/MCP.md](/Users/irinery/Documents/middlewareAuth/docs/MCP.md).

Para clientes MCP novos, use o contrato generico `llm_*` descrito em [docs/LLM_PROVIDER_CONTRACT.md](/Users/irinery/Documents/middlewareAuth/docs/LLM_PROVIDER_CONTRACT.md). O provider `openai` mapeia para as tools legadas `openai_*`/`codex_responses`, que seguem disponiveis por compatibilidade.
O provider `lmstudio` usa API key local salva criptografada pelo middleware e fala com a API OpenAI-compatible do LM Studio.

Para escolher comportamento tipo `Instant`/`Thinking` e esforco reflexivo, use `llm_responses` com `providerId`, `model`, `intelligence`, `reasoningEffort` ou `reasoning`. O wrapper aceita valores livres e `extra` para seletores futuros do backend.
