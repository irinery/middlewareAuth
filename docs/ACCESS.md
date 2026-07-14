# Acesso local

Os exemplos abaixo usam somente a API canonica `llm/*`. O contrato completo e
os payloads prontos para frontend ficam em
[`LLM_PROVIDER_CONTRACT.md`](./LLM_PROVIDER_CONTRACT.md) e
[`examples/llm-http-payloads.json`](./examples/llm-http-payloads.json).

Porta default: `18787`.
Bind default: `127.0.0.1`.

Config minima:

```sh
export MIDDLEWARE_SECRET_KEY="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_CLIENT_TOKEN="$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
export MIDDLEWARE_STATE_DIR='/Users/irinery/Documents/middlewareAuth/.middleware-state'
export HTTP_BIND_ADDR='127.0.0.1'
export HTTP_PORT=18787
```

Nao crie `.env`: o repo bloqueia `.env*` e o check de seguranca falha se encontrar. Use path absoluto em `MIDDLEWARE_STATE_DIR` durante dev. Se usar `.middleware-state`, o store fica relativo ao `cwd` do processo.
Para expor fora do localhost, configure `HTTP_BIND_ADDR` e `MIDDLEWARE_ALLOW_NON_LOOPBACK_BIND=true` explicitamente.

`MIDDLEWARE_SECRET_KEY` e `MIDDLEWARE_CLIENT_TOKEN` precisam ter pelo menos 32 caracteres. O callback OAuth usa obrigatoriamente a mesma `HTTP_PORT`: existe somente um listener HTTP. A redacao de logs e obrigatoria; `MIDDLEWARE_REDACT_LOGS=false` impede o boot.

Subir o servico:

```sh
go run ./cmd/middleware-codex-oauth
```

Healthcheck sem auth:

```sh
curl -s http://localhost:18787/healthz
```

Catalogo de providers:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  "http://localhost:18787/v1/projects/acme/llm/providers"
```

Iniciar login OAuth pelo contrato canonico:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"providerId":"openai","profileId":"default","mode":"oauth"}' \
  http://localhost:18787/v1/projects/acme/llm/login
```

Consultar status operacional da sessao de login:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  "http://localhost:18787/v1/projects/acme/llm/login-sessions/<loginSessionId>?providerId=openai&profileId=default"
```

Iniciar device-code:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"providerId":"openai","profileId":"default","mode":"device_code"}' \
  http://localhost:18787/v1/projects/acme/llm/login
```

Chamar OpenAI depois de autenticar um perfil:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
	"providerId": "openai",
	"profileId": "default",
    "model": "gpt-5.5",
    "intelligence": "thinking",
    "reasoning": {"effort": "high"},
    "input": [{"role":"user","content":"responda ok"}],
    "stream": true
  }' \
  "http://localhost:18787/v1/projects/acme/llm/responses"
```

Pelo MCP, tambem da para usar `reasoningEffort="estendido"`; o wrapper converte esse alias para `reasoning.effort="high"`. Para valores novos do backend, passe `reasoning` ou `extra` diretamente.

Configurar LM Studio com API key:

```sh
curl -s \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
	"providerId": "lmstudio",
    "profileId": "default",
	"mode": "api_key",
	"authFields": {
	  "baseUrl": "http://127.0.0.1:1234",
	  "apiKey": "<secret>"
	}
  }' \
  http://localhost:18787/v1/projects/acme/llm/login
```

Chamar LM Studio pelo middleware:

```sh
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
  "http://localhost:18787/v1/projects/acme/llm/responses"
```

Logs locais:

```sh
MIDDLEWARE_SECRET_KEY="$MIDDLEWARE_SECRET_KEY" \
MIDDLEWARE_CLIENT_TOKEN="$MIDDLEWARE_CLIENT_TOKEN" \
MIDDLEWARE_STATE_DIR='/Users/irinery/Documents/middlewareAuth/.middleware-state' \
HTTP_BIND_ADDR='127.0.0.1' \
HTTP_PORT=18787 \
go run ./cmd/middleware-codex-oauth 2>&1 | tee /tmp/middleware-auth.log
```

Cada request gera uma linha `http_request` com `method`, `path`, `status`, `duration_ms` e `remote`. O header `Authorization` nao e logado.
Respostas JSON usam `Cache-Control: no-store`; nao as armazene em proxy ou navegador.

Check anti-segredo:

```sh
sh ./scripts/check-no-secrets.sh
```
