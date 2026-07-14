# middlewareAuth

Middleware Go independente para centralizar autenticacao, persistir perfis por
projeto e expor providers LLM por uma API HTTP/MCP normalizada. Aplicacoes
consumidoras nao precisam conhecer refresh token, API key ou protocolo nativo
dos providers.

O contrato canonico e versionado neste repositorio em
[docs/LLM_PROVIDER_CONTRACT.md](./docs/LLM_PROVIDER_CONTRACT.md).

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
export MIDDLEWARE_STATE_DIR="$PWD/.middleware-state"
export HTTP_BIND_ADDR='127.0.0.1'
export HTTP_PORT=18787
```

Nao use `.env`: arquivos `.env*` sao bloqueados no repo. Injete essas variaveis pelo shell, launchd, systemd, Docker/Kubernetes secret ou pelo cliente MCP.

`MIDDLEWARE_SECRET_KEY` com 32+ caracteres e `MIDDLEWARE_CLIENT_TOKEN` sao obrigatorios fora de `NODE_ENV=test`.
Em dev, prefira `MIDDLEWARE_STATE_DIR` absoluto para nao criar stores diferentes conforme o diretorio de execucao.
Por default o servidor escuta so em `127.0.0.1`; para expor em rede, configure `HTTP_BIND_ADDR` e `MIDDLEWARE_ALLOW_NON_LOOPBACK_BIND=true` de forma explicita.

## Rodar

Terminal 1: exporte as variaveis e suba o servidor HTTP local.

```sh
cd /caminho/para/middlewareAuth

export MIDDLEWARE_SECRET_KEY="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_CLIENT_TOKEN="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_STATE_DIR="$PWD/.middleware-state"
export HTTP_BIND_ADDR='127.0.0.1'
export HTTP_PORT=18787

go run ./cmd/middleware-codex-oauth
```

Terminal 2: valide que subiu.

```sh
curl -s http://localhost:18787/healthz
```

Para reutilizar o mesmo token em outro terminal, exporte o mesmo `MIDDLEWARE_CLIENT_TOKEN` que foi gerado no Terminal 1. Nao use `.env`.

Build opcional:

```sh
go build -o ./bin/middleware-codex-oauth ./cmd/middleware-codex-oauth
go build -o ./bin/middleware-codex-oauth-mcp ./cmd/middleware-codex-oauth-mcp
```

Rodar binario:

```sh
./bin/middleware-codex-oauth
```

Mais exemplos de acesso: [docs/ACCESS.md](./docs/ACCESS.md).

Endpoints principais:

```text
GET  http://localhost:18787/healthz
GET  http://localhost:18787/v1/projects/{projectId}/llm/providers
POST http://localhost:18787/v1/projects/{projectId}/llm/login
GET  http://localhost:18787/v1/projects/{projectId}/llm/login-sessions/{loginSessionId}
GET  http://localhost:18787/v1/projects/{projectId}/llm/status
POST http://localhost:18787/v1/projects/{projectId}/llm/refresh
POST http://localhost:18787/v1/projects/{projectId}/llm/responses
```

O contrato generico e seus payloads estao em [docs/LLM_PROVIDER_CONTRACT.md](./docs/LLM_PROVIDER_CONTRACT.md). Rotas especificas continuam disponiveis para compatibilidade:

```text
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

Exemplo local pelo contrato canonico:

```sh
curl -s http://localhost:18787/healthz

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  http://localhost:18787/v1/projects/acme/llm/providers

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"providerId":"openai","profileId":"default","mode":"device_code"}' \
  http://localhost:18787/v1/projects/acme/llm/login

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  "http://localhost:18787/v1/projects/acme/llm/login-sessions/<loginSessionId>?providerId=openai&profileId=default"
```

LM Studio via middleware:

```sh
export LMSTUDIO_BASE_URL='http://127.0.0.1:1234'
export LMSTUDIO_API_KEY='<api key do LM Studio>'

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
	\"providerId\": \"lmstudio\",
    \"profileId\": \"default\",
	\"mode\": \"api_key\",
	\"authFields\": {
	  \"baseUrl\": \"$LMSTUDIO_BASE_URL\",
	  \"apiKey\": \"$LMSTUDIO_API_KEY\"
	}
  }" \
  http://localhost:18787/v1/projects/acme/llm/login

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
	"providerId": "lmstudio",
	"profileId": "default",
    "model": "local-model",
    "input": [{"role":"user","content":"responda ok"}],
    "stream": false
  }' \
  http://localhost:18787/v1/projects/acme/llm/responses
```

## Verificacao

```sh
sh ./scripts/check-no-secrets.sh
jq empty ./docs/examples/llm-http-payloads.json
shellcheck -s sh ./scripts/e2e-live-lmstudio.sh
./scripts/verify.sh
```

## MCP

Servidor MCP local por stdio:

```sh
MIDDLEWARE_BASE_URL='http://localhost:18787' \
MIDDLEWARE_CLIENT_TOKEN="$MIDDLEWARE_CLIENT_TOKEN" \
MCP_DEFAULT_PROJECT_ID='acme' \
go run ./cmd/middleware-codex-oauth-mcp
```

O MCP nao sobe porta HTTP. Ele fala por stdin/stdout e chama o middleware HTTP local em `MIDDLEWARE_BASE_URL`.

Detalhes e config de cliente: [docs/MCP.md](./docs/MCP.md).

Para clientes MCP novos, use o contrato generico `llm_*` descrito em [docs/LLM_PROVIDER_CONTRACT.md](./docs/LLM_PROVIDER_CONTRACT.md). As tools genericas chamam a API HTTP canonica; `openai_*` e `codex_responses` seguem disponiveis apenas por compatibilidade.
O provider `lmstudio` usa API key local salva criptografada pelo middleware e fala com a API OpenAI-compatible do LM Studio.

Para escolher comportamento tipo `Instant`/`Thinking` e esforco reflexivo, use `llm_responses` com `providerId`, `model`, `intelligence`, `reasoningEffort` ou `reasoning`. O wrapper aceita valores livres e `extra` para seletores futuros do backend.
