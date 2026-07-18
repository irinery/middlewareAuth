# Contrato canonico de provedores LLM

Versao do contrato: `middlewareauth.llm.v1`.

Este arquivo e a fonte de verdade para aplicacoes que consomem o `middlewareAuth` por HTTP ou MCP. Os exemplos completos e prontos para fixture estao em [`examples/llm-http-payloads.json`](./examples/llm-http-payloads.json).

## Limite de responsabilidade

Cada projeto tem que funcionar sozinho. O `middlewareAuth` melhora a integracao entre os projetos, mas nunca pode ser dependencia bloqueante para build, testes ou funcionalidades locais de uma aplicacao consumidora.

O `middlewareAuth` e a caixa-preta responsavel por:

- autenticar e armazenar credenciais por `providerId + projectId + profileId`;
- fazer refresh, quando suportado;
- escolher e chamar o adapter interno do provider;
- proteger tokens e API keys;
- normalizar catalogo, status, login, resposta e erros.

A aplicacao consumidora e responsavel por:

- configurar apenas `baseURL`, token interno, `projectId`, `providerId` e `profileId`;
- montar a experiencia usando o catalogo e as capabilities publicas;
- decodificar o envelope generico e converte-lo para seu dominio;
- manter fluxo local/fallback quando o middleware estiver ausente ou indisponivel.

O consumidor nao deve conhecer rotas como `/codex`, `/lmstudio` ou `/auth/openai`, nem formatos nativos dos providers. Essas rotas continuam disponiveis apenas para compatibilidade com clientes legados.

## Convencoes HTTP

Base local padrao:

```text
http://127.0.0.1:18787
```

Toda rota de projeto exige:

```http
Authorization: Bearer <MIDDLEWARE_CLIENT_TOKEN>
Accept: application/json
```

Requests com corpo tambem usam `Content-Type: application/json`. `projectId`, `profileId` e `providerId` sao identificadores em minusculas, sem espacos. Todos os timestamps sao Unix epoch em milissegundos.

## Campos estaveis

Enquanto `contractVersion` continuar em `middlewareauth.llm.v1`, estes nomes e
significados sao estaveis: `providerId`, `projectId`, `profileId`,
`authenticated`, `status`, `loginSessionId`, `events`, `responseId`, `usage`,
`outputText`, `error.code` e `error.message`. Campos opcionais podem ser
omitidos quando nao se aplicam. Clientes precisam ignorar campos de resposta
desconhecidos para aceitar adicoes compativeis no v1.

Mudanca de nome, tipo ou semantica de um campo estavel exige nova versao major
do contrato. A ordem de propriedades JSON e a ordem dos providers nao fazem
parte do contrato.

## Valores provider-specific e metadata

Valores de `models[].id`, `auth.modes[]`, descritores de `auth.fields`,
`model`, `accountId`, `baseUrl`, `email` e `planType` podem variar por provider.
O cliente descobre esses valores pelo catalogo e nao deve codificar regras como
`if providerId == "lmstudio"`.

`authFields` e o unico container de entrada para valores dinamicos de
autenticacao. `extra` e um escape hatch provider-specific para inferencia; ele
nao pode sobrescrever campos estaveis e so deve ser usado quando uma capability
ou documentacao do adapter exigir. `metadata` fica reservado como container de
saida para extensoes futuras: suas chaves internas nao sao estaveis e o cliente
nao pode depender delas para o fluxo principal. O runtime atual nao precisa
emitir `metadata`.

## Catalogo

```http
GET /v1/projects/{projectId}/llm/providers
```

Resposta enquanto pendente:

```json
{
  "providerId": "openai",
  "projectId": "pockettrace",
  "profileId": "default",
  "loginSessionId": "sess_123",
  "mode": "device_code",
  "status": "pending",
  "authenticated": false,
  "verificationUrl": "https://auth.example/device",
  "userCode": "ABCD-EFGH",
  "expiresAt": 1780000000000
}
```

Enquanto o status for `pending`, o polling repete `authUrl` ou o par
`verificationUrl`/`userCode` retornado na criacao. Isso permite que clientes
reconstruam a interface depois de refresh, reabertura da tela ou perda da
resposta inicial. Esses campos somem ao concluir, falhar ou expirar a sessao.

Resposta concluida:

```json
{
  "contractVersion": "middlewareauth.llm.v1",
  "providers": [
    {
      "id": "openai",
      "title": "OpenAI",
      "auth": {
        "required": true,
        "modes": ["oauth", "device_code"],
        "defaultMode": "device_code",
        "fields": []
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
        "refresh": true,
        "intelligence": true,
        "reasoningEffort": true,
        "systemInstructions": true,
        "tools": false,
        "store": true
      }
    }
  ]
}
```

A UI deve derivar os controles de `auth`, `auth.fields`, `defaults`, `models` e `capabilities`; nao deve condicionar comportamento ao nome do provider. Um field descreve `id`, `title`, `type`, `required` e `secret`. `models` pode estar vazio, especialmente para providers locais. Nesse caso, aceite um model informado pelo usuario.

Semantica das capabilities:

| Campo | Quando `true` |
| --- | --- |
| `stream` | O adapter pode pedir streaming ao provider; a resposta HTTP continua sendo um JSON normalizado e agregado. |
| `refresh` | A UI pode exibir acao de refresh. |
| `intelligence` | A UI pode enviar o seletor livre `intelligence`. |
| `reasoningEffort` | A UI pode enviar `reasoning` ou o alias MCP `reasoningEffort`. |
| `systemInstructions` | A UI pode enviar `instructions`. |
| `tools` | A UI pode enviar `tools`. |
| `store` | A UI pode oferecer o controle `store`. |

Se uma capability for `false`, o consumidor nao deve exibir nem enviar o
controle correspondente. O catalogo completo dos providers atuais esta no
fixture versionado e e comparado automaticamente com o runtime pelos testes.

## Login

```http
POST /v1/projects/{projectId}/llm/login
```

OAuth ou device code:

```json
{
  "providerId": "openai",
  "profileId": "default",
  "mode": "device_code"
}
```

API key configurada no middleware:

```json
{
  "providerId": "lmstudio",
  "profileId": "default",
  "mode": "api_key",
  "authFields": {
    "baseUrl": "http://127.0.0.1:1234",
    "apiKey": "<secret>"
  }
}
```

Resposta pendente:

```json
{
  "providerId": "openai",
  "projectId": "pockettrace",
  "profileId": "default",
  "loginSessionId": "sess_123",
  "mode": "device_code",
  "status": "pending",
  "authenticated": false,
  "verificationUrl": "https://auth.example/device",
  "userCode": "ABCD-EFGH",
  "expiresAt": 1780000000000
}
```

`authFields` recebe os valores dos IDs publicados pelo catálogo e permite adicionar providers sem mudar o cliente. Campos com `secret=true` devem existir apenas na memoria necessaria para enviar o request e nunca devem ser persistidos pelo frontend, incluidos em URL, log ou telemetria. Dependendo do modo, podem existir `authUrl`, `verificationUrl`, `userCode`, `baseUrl`, `accountId`, `modelCount` e `savedAt` na resposta. `apiKey`, access token e refresh token nunca voltam.

## Status da sessao de login

```http
GET /v1/projects/{projectId}/llm/login-sessions/{loginSessionId}?providerId={providerId}&profileId={profileId}
```

Resposta:

```json
{
  "providerId": "openai",
  "projectId": "pockettrace",
  "profileId": "default",
  "loginSessionId": "sess_123",
  "mode": "device_code",
  "status": "authenticated",
  "authenticated": true,
  "expiresAt": 1780000000000,
  "completedAt": 1779999900000
}
```

Valores atuais de `status`: `pending`, `authenticated`, `expired` e `failed`.
Nos dois ultimos casos, a resposta inclui `error` ja normalizado para um codigo
`ERR_LLM_*`; o consumidor nunca precisa tratar `ERR_LOGIN_*` ou erros nativos do
provider. Exemplos de todos os estados estao no fixture versionado.

## Status da credencial

```http
GET /v1/projects/{projectId}/llm/status?providerId={providerId}&profileId={profileId}
```

Resposta:

```json
{
  "authenticated": true,
  "providerId": "openai",
  "projectId": "pockettrace",
  "profileId": "default",
  "accountId": "account-123",
  "email": "user@example.com",
  "planType": "plus",
  "expires": 1780000000000
}
```

`accountId`, `email`, `planType`, `baseUrl` e `expires` sao opcionais.

## Refresh

```http
POST /v1/projects/{projectId}/llm/refresh
```

Request:

```json
{
  "providerId": "openai",
  "profileId": "default"
}
```

A resposta usa o mesmo contrato de status. Se o provider nao suportar refresh, o middleware retorna `ERR_LLM_REFRESH_UNSUPPORTED`.

## Inferencia

```http
POST /v1/projects/{projectId}/llm/responses
```

Request:

```json
{
  "providerId": "openai",
  "profileId": "default",
  "model": "gpt-5.5",
  "instructions": "Responda de forma objetiva.",
  "input": [
    { "role": "user", "content": "Responda apenas: ok" }
  ],
  "stream": true,
  "store": false,
  "intelligence": "thinking",
  "reasoning": {
    "effort": "medium",
    "summary": "auto"
  }
}
```

`providerId`, `profileId`, `model` e `input` sao obrigatorios. `instructions`, `stream`, `store`, `intelligence`, `reasoning` e `tools` sao opcionais e so devem ser enviados quando a capability correspondente permitir. O model e uma string livre para nao bloquear novos modelos. Campos adicionais podem ser enviados pelo HTTP no top-level e pelo MCP dentro de `extra`; adapters ignoram ou encaminham esses campos conforme sua implementacao.

Resposta normalizada:

```json
{
  "events": [
    {
      "type": "response.output_text.delta",
      "payload": "{\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}"
    },
    { "type": "done" }
  ],
  "responseId": "resp_123",
  "usage": {
    "inputTokens": 10,
    "outputTokens": 1,
    "totalTokens": 11
  },
  "outputText": "ok"
}
```

O consumidor deve preferir `outputText`. `events` existe para progresso/compatibilidade e `usage` e opcional.

## Erros

Formato unico:

```json
{
  "error": {
    "code": "ERR_LLM_AUTH_REQUIRED",
    "message": "autenticacao do provider necessaria"
  }
}
```

Codigos publicos:

| Codigo | HTTP esperado | Tratamento do consumidor |
| --- | ---: | --- |
| `ERR_LLM_PROVIDER_UNKNOWN` | 400 | Recarregar catalogo ou impedir selecao invalida. |
| `ERR_LLM_AUTH_REQUIRED` | 401 | Iniciar login/configuracao. |
| `ERR_LLM_AUTH_EXPIRED` | 401 | Reautenticar. |
| `ERR_LLM_REFRESH_UNSUPPORTED` | 400 | Ocultar refresh; o catalogo deve publicar `refresh=false`. |
| `ERR_LLM_REQUEST_INVALID` | 400/413 | Corrigir ou limitar payload. |
| `ERR_LLM_PROVIDER_UNAVAILABLE` | 408/502/504 | Acionar fallback local do consumidor. `408` indica cancelamento, `504` timeout e `502` indisponibilidade/falha 5xx do provider. |
| `ERR_LLM_RATE_LIMITED` | 429 | Aplicar retry/backoff no consumidor. |
| `ERR_LLM_RESPONSE_EMPTY` | 502 | Tratar como falha transitoria/fallback. |
| `ERR_LLM_INTERNAL` | 500 | Tratar como falha transitoria/fallback. |

`error.details` e opcional e serve apenas para validacao de campos. No MCP,
`providerId`, `projectId` e `profileId` podem aparecer dentro de `error` como
contexto adicional; o cliente continua tomando decisao somente por `code`.

A aplicacao deve tratar codigo, nao texto. Ausencia do middleware, timeout e `ERR_LLM_PROVIDER_UNAVAILABLE` devem acionar o fallback proprio do consumidor, sem bloquear seu fluxo principal.

## MCP

As tools genericas espelham o contrato HTTP:

```text
llm_providers
llm_login_start
llm_login_status
llm_status
llm_refresh
llm_responses
```

No MCP, `projectId` vai nos argumentos da tool. No HTTP, ele vai no path. Novos consumidores devem usar apenas `llm_*`. As tools e rotas `openai_*`, `codex_responses`, `/auth/openai`, `/codex`, `/auth/lmstudio` e `/lmstudio` sao legadas e ficam restritas a compatibilidade.

`llm_providers` tambem recebe `projectId`; a tool consulta o endpoint HTTP
canonico em vez de manter um catalogo duplicado. Em todas as tools, o resultado
fica em `result.content[0].text` como JSON do mesmo contrato HTTP. Os argumentos
completos das seis tools estao no fixture
[`examples/llm-http-payloads.json`](./examples/llm-http-payloads.json).

## Politica de compatibilidade e depreciacao

As rotas `/v1/projects/{projectId}/llm/*`, as tools `llm_*` e o
`contractVersion` publicado pelo catalogo sao a API canonica. Novos consumidores
nao podem usar rotas ou tools provider-specific.

As rotas `/auth/openai/*`, `/codex/responses`, `/auth/lmstudio/*` e
`/lmstudio/responses`, junto das tools `openai_*` e `codex_responses`, sao
legadas. Elas recebem apenas correcoes de seguranca e compatibilidade, nao novas
features. Uma remocao exige nova versao major, aviso no changelog e ao menos uma
release de transicao. Nao existe data de remocao definida no v1.

Consumidores apenas referenciam este contrato; nao copiam logica de provider.
O build, os testes e o runtime do middleware usam somente arquivos e
dependencias deste repositorio. Da mesma forma, a indisponibilidade do
middleware deve degradar apenas a integracao opcional, nunca impedir que o
consumidor inicie ou execute suas funcoes locais.
