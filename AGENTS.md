# Instruções para agentes no middlewareAuth

## Independência do projeto

Cada projeto tem que funcionar sozinho.

O `middlewareAuth` pode oferecer integração para PocketTrace, PocketWiki e outros consumidores, mas não pode depender do checkout, build, runtime, arquivos internos ou disponibilidade desses projetos.

Regras obrigatórias:

- contratos públicos HTTP/MCP pertencem a este repositório;
- comportamento específico de um consumidor não entra no core do middleware;
- build e testes usam apenas dependências declaradas pelo próprio projeto;
- providers são acessados por adapters internos e respostas normalizadas;
- integrações com aplicações consumidoras são opcionais e testadas por contrato;
- indisponibilidade de um consumidor nunca impede o middleware de iniciar ou atender outros projetos;
- exemplos podem citar consumidores, mas a documentação canônica da API fica aqui.
