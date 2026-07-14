# Evidencia da issue #3: API LLM generica

Execucao: `2026-07-14T00:11:39Z`

Ambiente validado:

- middleware HTTP: binario compilado a partir do worktree;
- bridge MCP: binario compilado a partir do mesmo worktree;
- provider real: LM Studio `0.4.19+2`, com autenticacao obrigatoria;
- modelo real: `liquid/lfm2.5-1.2b`;
- credencial do provider: token dedicado no Keychain, nunca gravado no repositorio;
- estado do middleware: diretorio temporario `0700`, destruido ao final.

Comando reproduzivel:

```sh
LMSTUDIO_API_KEY="$(security find-generic-password \
  -a middlewareAuth -s middlewareAuth-e2e -w)" \
LMSTUDIO_MODEL='liquid/lfm2.5-1.2b' \
./scripts/e2e-live-lmstudio.sh
```

Resultado: `passed`.

Checks executados sem mock:

- autenticacao direta no provider real;
- catalogo HTTP canonico;
- login, login status, status e responses HTTP genericos;
- isolamento de credencial entre dois `projectId`;
- compatibilidade das rotas HTTP legadas;
- initialize e tools/list pelo protocolo MCP stdio;
- catalogos HTTP e MCP byte-equivalentes depois de normalizar JSON;
- login, login status, status e responses pelas tools MCP `llm_*`;
- ausencia da API key nas respostas HTTP/MCP;
- ausencia da API key em texto claro no estado persistido.

Saida resumida do runner:

```json
{
  "suite": "middlewareauth-live-lmstudio",
  "status": "passed",
  "finishedAt": "2026-07-14T00:11:39Z",
  "provider": "lmstudio",
  "model": "liquid/lfm2.5-1.2b",
  "projectId": "middlewareauth-e2e",
  "checks": 16
}
```

O runner gera segredos efemeros para o middleware, compila ambos os binarios,
inicia um servidor real em loopback e remove todo o estado temporario ao sair.
