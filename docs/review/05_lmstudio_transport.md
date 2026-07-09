# 05 - Integracao LM Studio

## Achado

`baseUrl` aceitava query, fragmento, userinfo e portas ambiguas. Respostas SSE sem `Content-Type: text/event-stream` eram tratadas como JSON comum, e o parser de stream nao preservava ID ou usage. O stream agregado podia crescer alem do limite total de memoria.

## Correcao

O provider aceita somente URL HTTP(S) local ou privada, sem userinfo, query, fragmento, porta invalida ou endereco nao especificado. API keys sao normalizadas antes de uso e persistencia. Toda resposta LM Studio tem limite total de 5 MiB; SSE e detectado tambem pelo corpo, normalizado para eventos internos e preserva `responseId`, `usage` e `outputText`.

## Validacao

- `TestValidateBaseURLRejectsAmbiguousParts`
- `TestTransportDoesNotFollowRedirectsWithAPIKey`
- `TestTransportParsesMislabelledSSEAndKeepsMetadata`
- `TestTransportListModelsAndSendResponse`
- `go test ./...`
