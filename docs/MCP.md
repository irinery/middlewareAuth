# MCP

Este repo inclui um servidor MCP local por `stdio`:

```text
cmd/middleware-codex-oauth-mcp
```

Ele segue o transporte `stdio` do MCP: stdin/stdout carregam somente mensagens JSON-RPC, e logs vao para stderr.

Build:

```sh
go build -o ./bin/middleware-codex-oauth-mcp ./cmd/middleware-codex-oauth-mcp
```

Variaveis usadas pelo MCP:

```sh
export MIDDLEWARE_BASE_URL='http://localhost:18787'
export MIDDLEWARE_CLIENT_TOKEN='<token gerado para o middleware>'
export MCP_DEFAULT_PROJECT_ID='acme'
export MIDDLEWARE_LLM_PROVIDER='openai'
export MIDDLEWARE_LLM_PROFILE_ID='default'
export MIDDLEWARE_LLM_MODEL='gpt-5.5'
```

Nao use `.env` para essas variaveis. Passe pelo gerenciador do cliente MCP ou pelo ambiente do processo.

O MCP so aceita `MIDDLEWARE_BASE_URL` em loopback (`localhost`, `127.0.0.1` ou `::1`) e exige `MIDDLEWARE_CLIENT_TOKEN` com 32 ou mais caracteres. Se alguma dessas condicoes falhar, o processo encerra no startup com erro de configuracao em stderr.

Ferramentas expostas:

```text
middleware_health
llm_providers
llm_login_start
llm_login_status
llm_status
llm_refresh
llm_responses
openai_login_start
openai_login_status
openai_status
openai_refresh
codex_responses
```

Clientes novos devem usar `llm_*`. As ferramentas `openai_*` e
`codex_responses` continuam apenas por compatibilidade. As tools genericas
chamam `/v1/projects/{projectId}/llm/*`; autenticacao e dispatch de provider
ficam no servidor HTTP canonico.

Defaults MCP:

```text
providerId  MIDDLEWARE_LLM_PROVIDER, fallback openai
profileId  MIDDLEWARE_LLM_PROFILE_ID, fallback MCP_OPENAI_PROFILE_ID, fallback default
model      MIDDLEWARE_LLM_MODEL, fallback MCP_OPENAI_MODEL, fallback gpt-5.5
projectId  MCP_DEFAULT_PROJECT_ID, se o cliente nao enviar projectId
```

Para LM Studio, use `providerId="lmstudio"` e faca um setup inicial com `mode="api_key"`.
A API key e enviada ao middleware e salva criptografada; ela nao volta em `status` nem em `responses`.
Os campos de autenticacao sempre ficam em `authFields`, usando os IDs publicados
por `llm_providers`.

Exemplo de setup LM Studio:

```json
{
  "providerId": "lmstudio",
  "projectId": "acme",
  "profileId": "default",
  "mode": "api_key",
  "authFields": {
    "baseUrl": "http://127.0.0.1:1234",
    "apiKey": "<secret>"
  }
}
```

Depois chame:

```json
{
  "providerId": "lmstudio",
  "projectId": "acme",
  "profileId": "default",
  "model": "local-model",
  "input": "Responda apenas: ok lmstudio"
}
```

O contrato generico completo fica em
[`docs/LLM_PROVIDER_CONTRACT.md`](./LLM_PROVIDER_CONTRACT.md).

## Modelo, inteligencia e esforco

Na tool `llm_responses`, separe estes conceitos:

```text
providerId         provider LLM; hoje openai
model              versao/familia do modelo, exemplo: gpt-5.5
intelligence       nivel livre do backend, exemplo atual: instant ou thinking
reasoningEffort    esforco reflexivo; aliases: padrao -> medium, estendido -> high
reasoning          objeto bruto para quem quiser passar o formato nativo do backend
extra              campos top-level futuros, repassados sem sobrescrever campos conhecidos
```

Exemplo equivalente ao seletor visual `5.5` + `Thinking` + `Estendido`:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "model": "gpt-5.5",
  "intelligence": "thinking",
  "reasoningEffort": "estendido",
  "input": "Responda apenas: ok pocketwiki"
}
```

Se a OpenAI mudar nomes ou adicionar seletores, use valores nativos diretamente ou `extra`:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "model": "gpt-5.5",
  "intelligence": "research",
  "reasoning": { "effort": "extended" },
  "extra": {
    "futureSelector": "valor-novo"
  },
  "input": "teste"
}
```

Exemplo de config para clientes MCP locais:

```json
{
  "mcpServers": {
    "middleware-auth": {
      "command": "/caminho/absoluto/middlewareAuth/bin/middleware-codex-oauth-mcp",
      "env": {
        "MIDDLEWARE_BASE_URL": "http://localhost:18787",
        "MIDDLEWARE_CLIENT_TOKEN": "<token gerado para o middleware>",
        "MCP_DEFAULT_PROJECT_ID": "acme",
        "MIDDLEWARE_LLM_PROVIDER": "openai",
        "MIDDLEWARE_LLM_PROFILE_ID": "default",
        "MIDDLEWARE_LLM_MODEL": "gpt-5.5"
      }
    }
  }
}
```

Teste manual por stdio:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"manual","version":"0.1.0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"llm_providers","arguments":{"projectId":"acme"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"llm_login_status","arguments":{"providerId":"openai","projectId":"acme","loginSessionId":"<loginSessionId>"}}}' \
  | MIDDLEWARE_BASE_URL='http://localhost:18787' \
    MIDDLEWARE_CLIENT_TOKEN="$MIDDLEWARE_CLIENT_TOKEN" \
    go run ./cmd/middleware-codex-oauth-mcp
```

Teste com MCP Inspector:

```sh
npx -y @modelcontextprotocol/inspector \
  go run ./cmd/middleware-codex-oauth-mcp
```

No Inspector, configure as envs `MIDDLEWARE_BASE_URL`, `MIDDLEWARE_CLIENT_TOKEN` e `MCP_DEFAULT_PROJECT_ID` antes de chamar tools protegidas.

Referencias usadas:

- https://modelcontextprotocol.io/docs/getting-started/intro
- https://modelcontextprotocol.io/docs/develop/build-server
- https://modelcontextprotocol.io/specification/2025-11-25/basic/transports
- https://modelcontextprotocol.io/specification/2025-11-25/server/tools
