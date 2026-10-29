# Contrato canonico de provedores de decisao

Versao do contrato: `middlewareauth.decisions.v1`.

Jev e um modelo de decisao tipada, nao conversacional: recebe um `state` e um
mapa de perguntas (`noul`, `choice` ou `score`) e retorna respostas tipadas com
probabilidades. Ele fica em uma API separada do catalogo e das rotas LLM.

Todas as rotas exigem `Authorization: Bearer <MIDDLEWARE_CLIENT_TOKEN>` e
operam credenciais isoladas por `providerId + projectId + profileId`.

## Providers

```http
GET /v1/projects/{projectId}/decisions/providers?profileId=default
```

Retorna `contractVersion: middlewareauth.decisions.v1` e os providers
`typesafe-jev` (modelo default `jev-latest`) e `openrouter-jev` (modelo default
`~typesafe/jev-latest`). O catálogo deliberadamente não faz parte de
`llm/providers`.

## Configurar API key e consultar status

```http
POST /v1/projects/{projectId}/decisions/login
Content-Type: application/json
```

```json
{"providerId":"typesafe-jev","profileId":"default","apiKey":"<secret>"}
```

Para OpenRouter, use `providerId: "openrouter-jev"` e uma chave OpenRouter.
O middleware valida a chave com `GET /v1/models` na TypeSafe ou `GET /api/v1/key`
no OpenRouter e a salva criptografada. Status nunca devolve a chave:

```http
GET /v1/projects/{projectId}/decisions/status?providerId=typesafe-jev&profileId=default
```

## Executar decisão

```http
POST /v1/projects/{projectId}/decisions/evaluate
Content-Type: application/json
```

```json
{
  "providerId":"typesafe-jev",
  "profileId":"default",
  "state":{"ticket":"Cobrado duas vezes; solicito estorno."},
  "questions":{
    "refund":{"type":"noul","instructions":"A pessoa pede reembolso?"},
    "route":{"type":"choice","instructions":"Qual área deve atender?","criteria":{"billing":"Cobrança e pagamentos","support":"Outros assuntos"}},
    "urgency":{"type":"score","instructions":"Qual a urgência?","criteria":["baixa","média","alta"]}
  }
}
```

`model` é opcional e usa o default do provider. Resposta:

```json
{
  "providerId":"typesafe-jev",
  "projectId":"acme",
  "profileId":"default",
  "model":"jev-latest",
  "answers":{
    "refund":{"type":"noul","noul":0.98},
    "route":{"type":"choice","choice":"billing","probabilities":{"billing":0.96,"support":0.04},"confidence":0.96}
  },
  "usage":{"input_tokens":120,"output_tokens":12}
}
```

O `answers` tipado e o `usage` são preservados sem converter a saída para texto.
TypeSafe usa `POST https://api.typesafe.ai/v1/systemone`; OpenRouter usa
`POST https://openrouter.ai/api/alpha/decisions`. Os dois aceitam o mesmo corpo
de decisão, com IDs de modelo próprios de cada catálogo.

O provider `openrouter-jev` é intencionalmente distinto de `openrouter`:
`openrouter` é chat no contrato `middlewareauth.llm.v1`; `openrouter-jev` é
decisão e nunca aparece na lista de chatbots.

## MCP

O servidor MCP expõe `decision_providers`, `decision_login`, `decision_status`
e `decision_evaluate`, correspondentes às rotas acima. As ferramentas
`llm_providers` e `llm_responses` seguem exclusivas de modelos conversacionais.
