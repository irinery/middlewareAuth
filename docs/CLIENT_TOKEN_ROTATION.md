# Rotacao do token de cliente

`POST /v1/auth/client-token/rotate` troca o token interno global do MiddlewareAuth.
Nao recebe body nem `projectId`. Exige o token atual em
`Authorization: Bearer <token>` ou `X-Middleware-Token: <token>`.

Resposta `200`:

```json
{"clientToken":"<novo-token>"}
```

O servidor gera 32 bytes aleatorios e devolve o novo token apenas nessa
resposta, com `Cache-Control: no-store`. A troca e imediata: o token anterior
passa a receber `401 ERR_MIDDLEWARE_UNAUTHORIZED` em todas as rotas. Chamadas
concorrentes de rotacao com o mesmo token nao podem ter duas respostas de
sucesso. Erros de persistencia retornam `500` e mantem o token atual ativo.

O estado em `MIDDLEWARE_STATE_DIR/client-token.json` guarda somente o SHA-256
do token novo, com permissao `0600`. Na reinicializacao, esse estado prevalece
sobre `MIDDLEWARE_CLIENT_TOKEN`; a variavel de ambiente serve apenas como
credencial inicial antes da primeira rotacao. Preserve o mesmo state dir ao
reiniciar. O token novo deve ser salvo pelo cliente que pediu a rotacao e
distribuido aos demais consumidores; a API nao tem endpoint para recuperar
o valor original. Sem o token atual, uma aplicacao nao consegue se autenticar
nem solicitar outra rotacao.

Exemplo:

```sh
new_token=$(curl -fsS -X POST \
  -H "Authorization: Bearer $MIDDLEWARE_CLIENT_TOKEN" \
  http://127.0.0.1:18787/v1/auth/client-token/rotate | jq -er '.clientToken')
export MIDDLEWARE_CLIENT_TOKEN="$new_token"
```

O SDK Go oferece `client.Auth.RotateClientToken(ctx)`, que retorna o valor
novo e atualiza a instancia do client para as chamadas seguintes. O chamador
continua responsavel por persistir o valor retornado.
