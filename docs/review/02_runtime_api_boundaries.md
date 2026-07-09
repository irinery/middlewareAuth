# 02 - Limites de runtime e API

## Achado

O processo aceitava token interno curto, valores de timeout que podiam desativar limites HTTP, callback OAuth em porta sem listener correspondente e respostas JSON sem protecao explicita contra cache. A rota protegida comparava token com igualdade comum e o access log podia registrar path sem redacao.

## Correcao

`MIDDLEWARE_CLIENT_TOKEN` agora exige 32 ou mais caracteres, e a comparacao e feita em tempo constante. O callback deve usar a mesma porta do unico servidor HTTP. Timeouts, tamanho de payload, pool Codex e redacao de logs sao validados no boot; redacao nao pode ser desabilitada. A API fixa `Cache-Control: no-store`, `Pragma: no-cache`, `Referrer-Policy: no-referrer` e `X-Content-Type-Options: nosniff` em toda resposta JSON. O servidor tambem limita headers a 64 KiB e redige paths no access log.

## Validacao

- `TestLoadConfigRejectsShortClientToken`
- `TestLoadConfigRejectsSeparateCallbackPort`
- `TestLoadConfigRejectsUnsafeRuntimeLimits`
- `TestLoadConfigRequiresLogRedaction`
- `TestJSONResponsesSetNoStoreSecurityHeaders`
- `go test ./...`
- `go vet ./...`
