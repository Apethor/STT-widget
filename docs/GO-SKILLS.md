# Skills de orientação Go

O pacote selecionado é **samber/cc-skills-golang**, de uso geral.
As skills orientam estilo, organização de pacotes, interfaces pequenas,
erros, contexto/cancelamento, concorrência e testes.
São orientação da comunidade, não dependências de compilação nem ferramentas
que precisem acompanhar o ZIP.

## Instalação compartilhada

Foram instalados os **30 módulos gerais**, suas referências e a licença MIT
na área do Codex, fora do projeto:

- Fonte: <https://github.com/samber/cc-skills-golang>.
- Revisão registrada: `8e899e20ff0cd4dc524af3993e4c62d8ee8c5717`.
- Destino: `%USERPROFILE%\.codex\skills`.
- Registro: `.install-records\samber-cc-skills-golang.json` nesse destino.

A remoção do STT-widget e de suas ferramentas locais não removeu essas skills.
Sua disponibilidade depende do agente e da sessão usados.
Não reinstale ou remova a instalação compartilhada apenas para compilar o app.

## Aplicação no projeto

Prefira propagação de `context.Context`, goroutines com ciclo de vida
definido, isolamento por aba/geração, tratamento explícito de erros e
testes do resultado observável.
Regras opinativas e exemplos devem ser confrontados com o código existente,
os contratos em [AGENTS.md](../AGENTS.md), as decisões do usuário e a
documentação compatível com a versão Go de `go.mod`.

Referências:

- [Estilo](https://github.com/samber/cc-skills-golang/blob/main/skills/golang-code-style/SKILL.md).
- [Contexto](https://github.com/samber/cc-skills-golang/blob/main/skills/golang-context/SKILL.md).
- [Concorrência](https://github.com/samber/cc-skills-golang/blob/main/skills/golang-concurrency/SKILL.md).
- [Testes](https://github.com/samber/cc-skills-golang/blob/main/skills/golang-testing/SKILL.md).
- [Documentação Go](https://go.dev/doc/).
- [Effective Go](https://go.dev/doc/effective_go), que não cobre sozinho
  todos os recursos recentes da linguagem.
