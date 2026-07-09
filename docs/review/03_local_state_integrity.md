# 03 - Integridade do estado local

## Achado

O diretorio de estado, o store criptografado e o audit log aceitavam caminhos simbolicos ou tipos de arquivo inesperados. Isso permitia que um processo iniciado com configuracao local adulterada lesse ou gravasse fora do diretorio de estado esperado. O audit log tambem usava identificador previsivel e nao sincronizava a escrita.

## Correcao

O boot exige que o state dir seja um diretorio dedicado e real, sem symlink. O store e o audit log exigem arquivo regular; schema e quantidade maxima de perfis sao checados antes de descriptografar. O audit log ganhou ID aleatorio criptografico e `fsync` antes de confirmar o evento.

## Validacao

- `TestLoadConfigRejectsSymbolicLinkStateDir`
- `TestFileStoreRejectsSymbolicLink`
- `TestAuditRejectsSymbolicLink`
- `TestFileStoreSaveEncryptsAndLoadsProfile`
- `TestAuditEventDoesNotLeakTokens`
- `go test ./...`
