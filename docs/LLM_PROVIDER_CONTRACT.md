# Contrato tecnico: provedores LLM no middlewareAuth

Este documento define um contrato MCP generico para provedores de LLM no `middlewareAuth`.
Ele nao depende de PocketWiki, Codex Desktop, UI especifica ou qualquer cliente consumidor.

O objetivo e permitir que novos provedores sejam adicionados ao middleware sem criar uma tool MCP nova para cada tela ou app.

## Conceitos

### Provider

Um provider e uma integracao LLM autenticavel ou configuravel pelo middleware.

Regras:

- `providerId` e estavel, minusculo e sem espacos. Exemplos: `openai`, `anthropic`, `google`, `azure-openai`.
- `profileId` identifica uma credencial/conta dentro de um projeto.
- `projectId` separa tenants/projetos do middleware.
- O middleware e dono do ciclo de auth, refresh, armazenamento de token e chamada ao backend do provider.
- O cliente MCP nao deve receber access token, refresh token ou segredo bruto.

### Modelo

`model` e sempre uma string livre e provider-specific.

Exemplos:

```text
gpt-5.5
lmstudio/local-model
claude-4.5-sonnet
gemini-2.5-pro
```

O middleware pode expor presets no catalogo de providers, mas deve aceitar `model` livre em `llm_responses` para nao bloquear modelos novos.

### Raciocinio

Campos de comportamento devem ser opcionais.

Campos recomendados:

```json
{
  "intelligence": "thinking",
  "reasoningEffort": "medium",
  "reasoning": { "effort": "medium" }
}
```

`reasoningEffort` e o campo portavel. `reasoning` e o objeto bruto para providers que tenham formato proprio.

## Tools MCP genericas

As tools genericas recomendadas sao:

```text
llm_providers
llm_login_start
llm_login_status
llm_status
llm_refresh
llm_responses
```

As tools atuais especificas de OpenAI podem continuar existindo por compatibilidade:

```text
openai_login_start
openai_login_status
openai_status
openai_refresh
codex_responses
```

Implementacao recomendada: as tools legadas chamam internamente as genericas usando `providerId = "openai"`.

## `llm_providers`

Lista providers suportados e suas capacidades.

Entrada:

```json
{}
```

Saida:

```json
{
  "providers": [
    {
      "id": "openai",
      "title": "OpenAI",
      "auth": {
        "required": true,
        "modes": ["oauth", "device_code"],
        "defaultMode": "device_code"
      },
      "defaults": {
        "profileId": "default",
        "model": "gpt-5.5"
      },
      "models": [
        { "id": "gpt-5.5", "title": "gpt-5.5" },
        { "id": "gpt-5", "title": "gpt-5" }
      ],
      "capabilities": {
        "stream": true,
        "reasoningEffort": true,
        "systemInstructions": true,
        "tools": false
      }
    },
    {
      "id": "lmstudio",
      "title": "LM Studio",
      "auth": {
        "required": true,
        "modes": ["api_key"],
        "defaultMode": "api_key"
      },
      "defaults": {
        "profileId": "default",
        "model": "local-model"
      },
      "models": [
        { "id": "local-model", "title": "local-model" }
      ],
      "capabilities": {
        "stream": true,
        "reasoningEffort": false,
        "systemInstructions": true,
        "tools": false
      }
    }
  ]
}
```

Campos obrigatorios por provider:

- `id`
- `title`
- `auth.required`
- `defaults.profileId`
- `defaults.model`
- `capabilities.stream`

## `llm_login_start`

Inicia login para um provider.

Entrada:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "mode": "device_code"
}
```

Saida:

```json
{
  "loginSessionId": "sess_123",
  "authUrl": "https://...",
  "verificationUrl": "https://...",
  "userCode": "ABCD-EFGH",
  "expiresAt": 1780000000
}
```

Notas:

- `authUrl` e usado quando o cliente pode abrir o navegador direto.
- `verificationUrl` + `userCode` e usado em device-code flow.
- `api_key` e usado por providers locais/API-key, como LM Studio; a chave entra no middleware e nao volta na resposta.
- `expiresAt` deve ser Unix epoch em segundos.

## `llm_login_status`

Consulta uma sessao de login iniciada.

Entrada:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "loginSessionId": "sess_123"
}
```

Saida:

```json
{
  "status": "pending",
  "authenticated": false,
  "profileId": "default"
}
```

Valores recomendados para `status`:

```text
pending
authenticated
expired
failed
```

## `llm_status`

Consulta se existe credencial valida para um provider/profile.

Entrada:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default"
}
```

Saida:

```json
{
  "authenticated": true,
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "accountId": "google-oauth2|123",
  "email": "user@example.com",
  "planType": "plus",
  "expires": 1780000000
}
```

Campos opcionais:

- `accountId`
- `email`
- `planType`
- `expires`
- `metadata`

## `llm_refresh`

Forca refresh da credencial, quando o provider suportar.

Entrada:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default"
}
```

Saida: mesmo formato de `llm_status`.

Se o provider nao suportar refresh, retornar erro `ERR_LLM_REFRESH_UNSUPPORTED`.

## `llm_responses`

Executa uma chamada LLM.

Entrada minima:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "model": "gpt-5.5",
  "instructions": "Voce e um assistente objetivo.",
  "input": [
    {
      "role": "user",
      "content": "Responda apenas: ok"
    }
  ],
  "stream": true,
  "store": false
}
```

Entrada com raciocinio:

```json
{
  "providerId": "openai",
  "projectId": "acme",
  "profileId": "default",
  "model": "gpt-5.5",
  "reasoningEffort": "medium",
  "reasoning": { "effort": "medium" },
  "input": "Responda apenas: ok"
}
```

Regras de entrada:

- `providerId`, `projectId`, `profileId` e `model` sao obrigatorios.
- `input` pode ser string simples ou array de mensagens.
- `instructions` e opcional.
- `stream` default recomendado: `true`.
- `store` default recomendado: `false`.
- Campos desconhecidos podem ser aceitos em `extra`.
- `extra` nunca deve sobrescrever campos conhecidos.

Saida recomendada:

```json
{
  "events": [
    {
      "type": "response.output_text.delta",
      "payload": "{\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}"
    },
    {
      "type": "done",
      "payload": ""
    }
  ],
  "responseId": "resp_123",
  "usage": {
    "inputTokens": 10,
    "outputTokens": 1,
    "totalTokens": 11
  }
}
```

Importante:

- `events[].payload` deve ser um JSON serializado do evento original ou uma string vazia.
- Evite retornar SSE aninhado dentro de `payload`; normalize para eventos individuais.
- Se o backend nativo so entrega SSE, o middleware deve fazer o parse e devolver cada evento no array `events`.
- Clientes podem tolerar SSE aninhado por compatibilidade, mas isso nao deve ser o formato novo.

## Erros

Formato de erro recomendado no texto da tool MCP:

```json
{
  "error": {
    "code": "ERR_LLM_AUTH_REQUIRED",
    "message": "login necessario",
    "providerId": "openai",
    "projectId": "acme",
    "profileId": "default"
  }
}
```

Codigos recomendados:

```text
ERR_LLM_PROVIDER_UNKNOWN
ERR_LLM_AUTH_REQUIRED
ERR_LLM_AUTH_EXPIRED
ERR_LLM_REFRESH_UNSUPPORTED
ERR_LLM_REQUEST_INVALID
ERR_LLM_PROVIDER_UNAVAILABLE
ERR_LLM_RATE_LIMITED
ERR_LLM_RESPONSE_EMPTY
ERR_LLM_INTERNAL
```

## Variaveis de ambiente

Variaveis comuns:

```sh
MIDDLEWARE_BASE_URL='http://localhost:18787'
MIDDLEWARE_CLIENT_TOKEN='<token gerado para o middleware>'
MCP_DEFAULT_PROJECT_ID='acme'
MIDDLEWARE_LLM_PROVIDER='openai'
MIDDLEWARE_LLM_PROFILE_ID='default'
MIDDLEWARE_LLM_MODEL='gpt-5.5'
```

Variaveis especificas por provider podem existir por compatibilidade:

```sh
MCP_OPENAI_PROFILE_ID='default'
MCP_OPENAI_MODEL='gpt-5.5'
MCP_LMSTUDIO_PROFILE_ID='default'
MCP_LMSTUDIO_MODEL='local-model'
LMSTUDIO_BASE_URL='http://127.0.0.1:1234'
LMSTUDIO_API_KEY='<api key enviada apenas ao middleware>'
```

Preferencia:

1. Variaveis genericas `MIDDLEWARE_LLM_*`
2. Variaveis especificas do provider
3. Defaults do `llm_providers`

## Store de credenciais

Cada credencial salva deve ter, no minimo:

```json
{
  "provider": "openai",
  "projectId": "acme",
  "profileId": "default",
  "accountId": "provider-account-id",
  "email": "user@example.com",
  "expires": 1780000000
}
```

Tokens e refresh tokens ficam em campos internos do middleware e nao entram nas respostas MCP.

Chave logica recomendada:

```text
provider + projectId + profileId
```

## Checklist para adicionar um provider

1. Registrar provider no catalogo retornado por `llm_providers`.
2. Implementar auth start/status/refresh se `auth.required = true`.
3. Persistir credencial usando `provider + projectId + profileId`.
4. Implementar adapter de request para `llm_responses`.
5. Normalizar stream nativo para `events[]`.
6. Mapear erros nativos para codigos `ERR_LLM_*`.
7. Adicionar teste MCP por stdio para `llm_status` e `llm_responses`.
8. Manter tool legada, se ja existir cliente consumindo formato antigo.

## Compatibilidade OpenAI atual

O provider `openai` deve mapear:

```text
llm_login_start(providerId=openai)  -> openai_login_start
llm_login_status(providerId=openai) -> openai_login_status
llm_status(providerId=openai)       -> openai_status
llm_refresh(providerId=openai)      -> openai_refresh
llm_responses(providerId=openai)    -> codex_responses
```

O provider `lmstudio` deve mapear:

```text
llm_login_start(providerId=lmstudio, mode=api_key) -> POST /auth/lmstudio/api-key
llm_status(providerId=lmstudio)                    -> GET  /auth/lmstudio/status
llm_refresh(providerId=lmstudio)                   -> ERR_LLM_REFRESH_UNSUPPORTED
llm_responses(providerId=lmstudio)                 -> POST /lmstudio/responses
```

Enquanto as tools genericas nao existirem, clientes podem chamar as tools legadas diretamente.
Quando as tools genericas existirem, novos clientes devem preferir `llm_*`.
