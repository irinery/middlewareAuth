# 04 - Ciclo de vida OAuth

## Achado

Quando o provedor devolvia `error=access_denied`, ou quando o device-code expirava, a sessao podia continuar como `pending` ou aparecer como falha generica. Dados transitorios de PKCE, incluindo verifier e URL com state, permaneciam em memoria apos uma sessao terminal.

## Correcao

Callback recusado e callback sem code agora marcam a sessao como `failed` com erro sanitizado. Timeout de device-code marca `expired`, como o contrato de status define. Toda transicao terminal remove o indice `state` e descarta os dados do fluxo PKCE, mantendo apenas os campos seguros consultaveis ate a expiracao da sessao.

## Validacao

- `TestCallbackDenialMarksSessionFailedWithoutLeakingProviderDetail`
- `TestDeviceCodeTimeoutMarksSessionExpired`
- `TestDeviceCodeFailureMarksSessionFailed`
- `TestDeviceCodeSuccessMarksSessionCompleted`
- `go test -race ./...`
