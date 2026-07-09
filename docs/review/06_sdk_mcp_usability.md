# 06 - SDK Go e MCP

## Achado

O SDK repetia `POST` automaticamente em erro transitorio, podendo criar sessoes OAuth ou geracoes duplicadas. Base URLs com query, fragmento ou userinfo eram aceitas. O MCP aceitava endpoint remoto e token curto ate a primeira chamada, produzindo erro operacional tardio e potencialmente enviando o bearer interno para fora do host local.

## Correcao

O SDK exige base URL sem partes ambiguas, token com 32 ou mais caracteres e context nao nulo. Ele so repete metodos seguros (`GET`, `HEAD`, `OPTIONS`), nunca `POST`. O MCP valida no startup que usa um endpoint loopback e token forte, retorna erro claro de configuracao e tambem bloqueia redirects. Respostas HTTP nao estruturadas viraram mensagens genericas no SDK, evitando ecoar corpo arbitrario para a aplicacao consumidora.

## Validacao

- `TestNewClientRejectsUnsafeBaseURLAndShortToken`
- `TestClientDoesNotRetryPOST`
- `TestClientDoesNotFollowRedirectsWithMiddlewareToken`
- `TestServeRejectsUnsafeMCPConfiguration`
- `go test ./...`
