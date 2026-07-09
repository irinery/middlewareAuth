# PocketWiki + middlewareAuth: relatorio de teste ponta a ponta

> Registro historico do teste de 2026-06-01. Os valores fracos e qualquer referencia a `1455` abaixo descrevem o incidente original e nao sao configuracao operacional. O fluxo atual usa somente `http://localhost:18787/v1/auth/openai/callback`; siga [docs/ACCESS.md](./ACCESS.md) para iniciar o middleware com secrets de 32+ caracteres.

Data do teste: 2026-06-01  
Projeto testado no middleware: `acme`  
Perfil testado: `default`  
Base URL do middleware: `http://localhost:18787`  
MCP usado: `/Users/irinery/Documents/middlewareAuth/bin/middleware-codex-oauth-mcp`

## Onde fica o `.middleware-state`

No teste, o middleware foi iniciado a partir de:

```sh
/Users/irinery/Documents/middlewareAuth
```

Com:

```sh
MIDDLEWARE_STATE_DIR='.middleware-state'
```

Como o path e relativo, o diretorio real ficou em:

```text
/Users/irinery/Documents/middlewareAuth/.middleware-state
```

Arquivos observados:

```text
/Users/irinery/Documents/middlewareAuth/.middleware-state/auth-profiles.json
/Users/irinery/Documents/middlewareAuth/.middleware-state/middleware.log
/Users/irinery/Documents/middlewareAuth/.middleware-state/middleware.pid
```

O arquivo que guarda o perfil OAuth criptografado e:

```text
/Users/irinery/Documents/middlewareAuth/.middleware-state/auth-profiles.json
```

Ponto importante: se o middleware for iniciado de outro diretorio com `MIDDLEWARE_STATE_DIR='.middleware-state'`, ele vai criar outro `.middleware-state` relativo ao novo `cwd`. Para evitar confusao, recomendo usar path absoluto em dev:

```sh
export MIDDLEWARE_STATE_DIR='/Users/irinery/Documents/middlewareAuth/.middleware-state'
```

## Ambiente usado

Variaveis usadas nos testes:

```sh
export MIDDLEWARE_SECRET_KEY='<secret seguro de 32+ caracteres>'
export MIDDLEWARE_CLIENT_TOKEN='<token seguro de 32+ caracteres>'
export MIDDLEWARE_STATE_DIR='.middleware-state'
export HTTP_PORT=18787
export MIDDLEWARE_BASE_URL='http://localhost:18787'
export MCP_DEFAULT_PROJECT_ID='acme'
```

Builds executados:

```sh
go build -o ./bin/middleware-codex-oauth-mcp ./cmd/middleware-codex-oauth-mcp
go build -o ./bin/middleware-codex-oauth ./cmd/middleware-codex-oauth
```

Suite Go executada:

```sh
go test ./...
```

Resultado: passou.

## Testes MCP basicos

### `tools/list`

Comando equivalente:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"pocketwiki-check","version":"0.1.0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | /Users/irinery/Documents/middlewareAuth/bin/middleware-codex-oauth-mcp
```

Resultado: ok. O MCP listou:

```text
middleware_health
openai_login_start
openai_status
openai_refresh
codex_responses
```

### `middleware_health`

Resultado final esperado:

```json
{
  "status": "ok",
  "checks": [
    {
      "name": "http",
      "status": "ok"
    }
  ]
}
```

Esse teste passou quando o middleware estava rodando em `:18787`.

### Erro de quoting em env

Na primeira tentativa, o env foi passado com aspas literais por erro de shell:

```text
MIDDLEWARE_BASE_URL='"'http://localhost:18787'"'
```

O MCP recebeu a URL com aspas dentro do valor e retornou:

```text
parse "\"'http://localhost:18787'\"/healthz": first path segment in URL cannot contain colon
```

Nao e bug do middleware. Foi erro de comando no teste. O formato correto e:

```sh
env MIDDLEWARE_BASE_URL=http://localhost:18787 \
  MIDDLEWARE_CLIENT_TOKEN='<token seguro de 32+ caracteres>' \
  MCP_DEFAULT_PROJECT_ID=acme \
  /Users/irinery/Documents/middlewareAuth/bin/middleware-codex-oauth-mcp
```

## Status OpenAI antes do login

Teste via MCP:

```text
openai_status(projectId=acme, profileId=default)
```

Resultado antes de autenticar:

```json
{
  "authenticated": false,
  "projectId": "acme",
  "profileId": "default"
}
```

Teste direto HTTP:

```sh
curl -sS -i \
  -H 'Authorization: Bearer <token seguro de 32+ caracteres>' \
  'http://localhost:18787/v1/projects/acme/auth/openai/status?profileId=default'
```

Resultado:

```json
{"authenticated":false,"projectId":"acme","profileId":"default"}
```

Ok para estado inicial.

## Problema 1: OAuth redireciona para porta/rota que nao existe

Ao chamar:

```text
openai_login_start(projectId=acme, profileId=default, mode=oauth)
```

O middleware gerou `authUrl` com:

```text
redirect_uri=http://localhost:1455/auth/callback
```

Mas o servidor real estava ouvindo em:

```text
http://localhost:18787
```

E a rota implementada no `middlewareAuth` e:

```text
GET /v1/auth/openai/callback
```

Ou seja, o browser voltou para:

```text
http://localhost:1455/auth/callback?...code=...&state=...
```

E o Safari mostrou:

```text
O Safari Nao Pode Conectar-se ao Servidor
localhost:1455/auth/callback
```

Diagnostico: o `redirect_uri` gerado pelo middleware nao bate com o servidor HTTP que esta realmente aceitando callback.

Arquivos relacionados:

```text
internal/config/config.go
internal/oauth/oauth_flow.go
internal/httpapi/server.go
internal/httpapi/routes_auth.go
```

Configuracao default atual:

```go
CallbackHost: get(env, "OAUTH_CALLBACK_HOST", "localhost")
CallbackPort: getInt(env, "OAUTH_CALLBACK_PORT", 1455)
CallbackPath: get(env, "OAUTH_CALLBACK_PATH", "/auth/callback")
```

Rota real atual:

```go
if r.URL.Path == "/v1/auth/openai/callback" {
    h.handleCallback(w, r)
    return
}
```

Correcao recomendada:

Usar por padrao:

```sh
OAUTH_CALLBACK_PORT=18787
OAUTH_CALLBACK_PATH=/v1/auth/openai/callback
```

Ou criar um listener separado em `1455` para callback local, se essa for a arquitetura desejada. Hoje o default aponta para uma rota que nao existe no servidor principal.

## Workaround usado para validar OAuth

Para validar o fluxo sem alterar a arquitetura do middleware, foi criado um proxy temporario em `127.0.0.1:1455`.

Ele recebeu:

```text
/auth/callback?code=...&state=...
```

E encaminhou para:

```text
http://localhost:18787/v1/auth/openai/callback?code=...&state=...
```

Esse workaround confirmou que o callback chegava ao middleware.

## Problema 2: `ERR_AUTH_STORE_WRITE_FAILED` por credencial incompleta

Depois de usar o proxy temporario de callback, o middleware chegou na troca de token, mas retornou:

```json
{"error":{"code":"ERR_AUTH_STORE_WRITE_FAILED","message":"credencial OAuth incompleta"}}
```

Origem do erro:

```go
func validateProfile(projectID string, profileID string, credential StoredOAuthCredential) error {
    ...
    if credential.Access == "" || credential.Refresh == "" || credential.Expires == 0 || credential.AccountID == "" {
        return security.NewError("ERR_AUTH_STORE_WRITE_FAILED", "credencial OAuth incompleta", http.StatusBadRequest)
    }
    return nil
}
```

O `AccountID` estava vazio.

Fluxo atual em `routes_auth.go`:

```go
identity, err := auth.ResolveAuthIdentity(credentials.Access, credentials.Email)
...
accountID := identity.AccountID
if accountID == "" {
    accountID = credentials.AccountID
}
```

O problema: a resposta de token nem sempre vem com `account_id`, e o parser de JWT so buscava claims como:

```text
https://api.openai.com/auth.chatgpt_account_id
chatgpt_account_id
chatgptAccountId
account_id
```

No teste real, o identificador aproveitavel veio como `sub`/usuario OAuth, entao `AccountID` ficava vazio e o store recusava salvar.

Patch aplicado localmente para validar:

```go
identity.AccountID = firstClaimString(claims,
    "https://api.openai.com/auth.chatgpt_account_id",
    "https://api.openai.com/auth.chatgpt_user_id",
    "https://api.openai.com/auth.user_id",
    "chatgpt_account_id",
    "chatgpt_user_id",
    "chatgptAccountId",
    "account_id",
    "user_id",
    "userId",
    "sub",
)
```

Tambem foi ajustado o refresh para preservar o `AccountID` antigo caso o access token novo nao traga o claim:

```go
identity, _ := ResolveAuthIdentity(parsed.AccessToken, credential.Email)
accountID := identity.AccountID
if accountID == "" {
    accountID = credential.AccountID
}
```

Teste adicionado localmente:

```go
func TestResolveAuthIdentityUsesSubjectFallback(t *testing.T)
```

Resultado apos patch local:

```text
go test ./...
```

Passou.

## Resultado apos corrigir AccountID localmente

Depois do patch local, rebuild e novo login OAuth com o proxy temporario:

```text
openai_status(projectId=acme, profileId=default)
```

Retornou:

```json
{
  "accountId": "google-oauth2|109346547854387910901",
  "authenticated": true,
  "expires": 1781146387634,
  "profileId": "default",
  "projectId": "acme"
}
```

Tambem foi criado:

```text
/Users/irinery/Documents/middlewareAuth/.middleware-state/auth-profiles.json
```

Permissao observada:

```text
-rw------- auth-profiles.json
```

Isso esta correto para material sensivel.

## Teste `codex_responses`

Depois do status autenticado, foi executado via MCP:

```json
{
  "projectId": "acme",
  "profileId": "default",
  "model": "gpt-5.5",
  "instructions": "Responda em portugues brasileiro informal.",
  "input": [
    {
      "role": "user",
      "content": "Responda apenas: ok pocketwiki"
    }
  ],
  "stream": true,
  "store": false
}
```

Resultado: passou.

O retorno continha deltas:

```text
ok
 pocket
wiki
```

Resposta final:

```text
ok pocketwiki
```

Isso validou:

```text
MCP -> middleware local -> auth profile salvo -> refresh/credential resolver -> Codex backend -> resposta SSE -> MCP
```

## Problema 3: `codex_responses` retorna SSE aninhado no payload

O MCP retornou um JSON com `events`, mas cada `event.payload` continha um bloco SSE inteiro como string:

```json
{
  "events": [
    {
      "type": "response",
      "payload": "event: response.created\ndata: {...}\n\nevent: response.output_text.delta\ndata: {...}\n\n"
    }
  ]
}
```

Esse formato e valido pelo estado atual do `middlewareAuth`, mas e ruim para cliente MCP simples, porque o cliente precisa parsear SSE dentro de JSON dentro de texto MCP.

Impacto encontrado no PocketWiki:

O parser Swift inicial esperava `payload` como JSON direto, entao autenticaria, chamaria Codex, mas poderia interpretar a resposta como vazia.

Correcao aplicada no PocketWiki:

O parser agora aceita os dois formatos:

```text
payload JSON direto
payload com bloco SSE aninhado
```

Teste adicionado no PocketWiki:

```text
middleware auth codex response parsing
```

Recomendacao para o middleware:

Normalizar a resposta do MCP para eventos ja quebrados, cada um com `payload` JSON direto, por exemplo:

```json
{
  "events": [
    {
      "type": "response.output_text.delta",
      "payload": "{\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}"
    }
  ]
}
```

Ou retornar um campo agregado simples alem dos eventos:

```json
{
  "outputText": "ok pocketwiki",
  "events": [...]
}
```

Isso reduziria muito a complexidade nos clientes.

## Device-code

Tambem foi testado:

```text
openai_login_start(projectId=acme, profileId=default, mode=device_code)
```

O MCP retornou algo como:

```json
{
  "verificationUrl": "https://auth.openai.com/codex/device",
  "userCode": "XMER-L2QH3",
  "loginSessionId": "...",
  "expiresAt": 1780282935474
}
```

O navegador mostrou:

```text
Iniciou sessao no Codex
Agora voce pode fechar esta pagina
```

Mas antes do patch do `AccountID`, o status continuou:

```json
{"authenticated":false,"projectId":"acme","profileId":"default"}
```

Nao foi revalidado device-code depois do patch de `AccountID`, porque o OAuth com proxy ja validou o store e o `codex_responses`.

Pontos para revisar no device-code:

1. Confirmar se o goroutine de polling esta logando erros de `PollDeviceCode`.
2. Confirmar se, apos a tela de sucesso, `saveOAuthCredentials` esta sendo chamado.
3. Confirmar se o mesmo bug de `AccountID` era o bloqueio.
4. Expor algum status de sessao de login ou erro de polling para o cliente MCP.

Hoje, se o polling falha, o cliente so ve `authenticated=false`, sem motivo operacional.

## Processo em background

Durante o teste, deixar o middleware rodando em background com `nohup` ou `launchctl submit` se mostrou instavel neste ambiente do Codex Desktop: o processo subia, respondia uma chamada, e encerrava sem erro claro no log.

Rodando em foreground, funcionou:

```sh
cd /Users/irinery/Documents/middlewareAuth
env \
  MIDDLEWARE_SECRET_KEY='<secret seguro de 32+ caracteres>' \
  MIDDLEWARE_CLIENT_TOKEN='<token seguro de 32+ caracteres>' \
  MIDDLEWARE_STATE_DIR='.middleware-state' \
  HTTP_PORT=18787 \
  ./bin/middleware-codex-oauth
```

Log observado:

```text
INFO middleware iniciado addr=:18787
INFO http_request method=GET path=/healthz status=200
INFO http_request method=GET path=/v1/projects/acme/auth/openai/status status=200
INFO http_request method=POST path=/v1/projects/acme/codex/responses status=200
```

Nao tratei isso como bug definitivo do middleware, porque pode ser comportamento do ambiente de execucao do Codex Desktop. Para uso local normal, recomendo rodar em terminal separado ou criar um LaunchAgent plist real.

## Validacoes finais no PocketWiki

Depois dos ajustes no cliente Swift:

```sh
swift build
./script/run_core_tests.sh
```

Resultados:

```text
swift build: ok
46 core tests passing
```

Anteriormente tambem tinha sido executado:

```sh
./script/run_integration_tests.sh
```

Resultado:

```text
4 integration tests passing
4 integration checks passing
```

## Resumo dos problemas a corrigir no middlewareAuth

1. `redirect_uri` default incompatível com a rota real do servidor.

Atual:

```text
http://localhost:1455/auth/callback
```

Servidor real:

```text
http://localhost:18787/v1/auth/openai/callback
```

2. Store exige `AccountID`, mas o parser nao aceitava claims comuns como `sub` ou `user_id`.

Erro observado:

```json
{"error":{"code":"ERR_AUTH_STORE_WRITE_FAILED","message":"credencial OAuth incompleta"}}
```

3. Refresh deve preservar `AccountID` existente se o token novo nao trouxer identificador.

4. Device-code precisa expor erro de polling/salvamento. Hoje ele pode parecer sucesso no navegador, mas o cliente so ve `authenticated=false`.

5. `codex_responses` via MCP retorna SSE aninhado em `payload`, aumentando complexidade dos clientes.

6. Para dev, usar `MIDDLEWARE_STATE_DIR` absoluto evitaria criar stores diferentes conforme o diretorio de execucao.

## Estado final do teste

Estado final validado com middleware rodando em foreground:

```text
healthz: ok
openai_status: authenticated=true
codex_responses: ok pocketwiki
auth-profiles.json: criado em /Users/irinery/Documents/middlewareAuth/.middleware-state/auth-profiles.json
```

O teste ponta a ponta foi considerado aprovado depois do patch local de `AccountID` e do workaround temporario para callback OAuth.
