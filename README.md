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
- rotacao autenticada do token interno, persistida entre reinicios;
- adapters Claude API e OpenRouter por API key no contrato LLM generico;
- adapters de decisao TypeSafe Jev e OpenRouter Jev em contrato separado do catalogo de chat;
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
GET  http://localhost:18787/v1/projects/{projectId}/llm/providers?profileId={profileId}
POST http://localhost:18787/v1/projects/{projectId}/llm/login
GET  http://localhost:18787/v1/projects/{projectId}/llm/login-sessions/{loginSessionId}
GET  http://localhost:18787/v1/projects/{projectId}/llm/status
POST http://localhost:18787/v1/projects/{projectId}/llm/refresh
POST http://localhost:18787/v1/projects/{projectId}/llm/responses
GET  http://localhost:18787/v1/projects/{projectId}/decisions/providers?profileId={profileId}
POST http://localhost:18787/v1/projects/{projectId}/decisions/login
GET  http://localhost:18787/v1/projects/{projectId}/decisions/status?providerId={providerId}&profileId={profileId}
POST http://localhost:18787/v1/projects/{projectId}/decisions/evaluate
POST http://localhost:18787/v1/auth/client-token/rotate
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

A rotacao do token global exige o valor atual e invalida o anterior
imediatamente. Contrato e exemplo em
[docs/CLIENT_TOKEN_ROTATION.md](./docs/CLIENT_TOKEN_ROTATION.md).

Exemplo local pelo contrato canonico:

```sh
curl -s http://localhost:18787/healthz

curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  "http://localhost:18787/v1/projects/acme/llm/providers?profileId=default"

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

Claude API e OpenRouter usam o mesmo endpoint `llm/login` com `mode: "api_key"` e `authFields.apiKey`. Para Claude, `authFields.workspaceId` e opcional quando a chave precisa selecionar um workspace. O middleware valida a chave no provider antes de salva-la criptografada, e usa `providerId: "anthropic"` ou `"openrouter"` em `llm/responses`. O `model` e livre; use o ID da API Anthropic ou o slug `autor/modelo` do OpenRouter. Exemplos estao no contrato canonico.

Neste projeto, somente o provider `openai` usa credenciais de assinatura, pelo fluxo OAuth/device code ja existente. `anthropic` (Claude) e `openrouter` aceitam exclusivamente chave de API. Tokens de assinatura Claude nao sao coletados nem usados pelo middleware, conforme a [politica de credenciais da Anthropic](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use).

O catalogo `llm_providers` descobre os modelos OpenAI liberados para o perfil autenticado, incluindo esforcos e tiers como `Fast`. Para selecionar, use `llm_responses` com `providerId`, `model`, `reasoningEffort`/`reasoning` e `serviceTier`; o wrapper aceita valores livres e `extra` para seletores futuros do backend.
O endpoint upstream usa `OPENAI_CODEX_MODELS_PATH=/codex/models` e `OPENAI_CODEX_CLIENT_VERSION=0.145.0` por default; ambos podem ser ajustados sem depender de uma instalacao local do Codex.

TypeSafe Jev e OpenRouter Jev ficam na API separada de decisoes tipadas (`decisions/*`), sem aparecer em `llm_providers`. Configure a chave com `providerId: "typesafe-jev"` ou `"openrouter-jev"` e avalie `state` + `questions` em `decisions/evaluate`. O contrato canonico e [docs/DECISION_PROVIDER_CONTRACT.md](./docs/DECISION_PROVIDER_CONTRACT.md).
