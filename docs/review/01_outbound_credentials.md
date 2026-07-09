# 01 - Credenciais em transporte

## Achado

Os clientes HTTP usados para OAuth, refresh, Codex, LM Studio, SDK e MCP podiam seguir redirects. Um upstream comprometido ou mal configurado poderia receber um request com codigo OAuth, refresh token, bearer Codex, API key LM Studio ou bearer interno em um destino diferente do configurado. O LM Studio tambem recebia o cliente HTTP geral, que respeita proxy de ambiente.

## Correcao

Todos os clientes que carregam credenciais agora retornam a resposta 3xx sem segui-la. OAuth, refresh e Codex clonam inclusive clientes injetados para aplicar essa politica. O transporte LM Studio usa um cliente dedicado sem proxy de ambiente quando chamado pela API HTTP, preservando apenas conexao direta para host local ou privado.

## Validacao

- `TestExchangeAuthorizationCodeDoesNotFollowRedirect`
- `TestTransportDoesNotFollowRedirectsWithAPIKey`
- `TestClientDoesNotFollowRedirectsWithMiddlewareToken`
- `go test ./...`
- `go test -race ./...`
