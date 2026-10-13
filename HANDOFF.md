# Handoff — STT-widget

Atualizado em **13 de outubro de 2026**.
Workspace: raiz desta cópia do projeto, Windows e PowerShell.

## Estado atual

O projeto foi transferido para outra máquina somente com a árvore de fontes,
sem a pasta `.git`. O usuário escolheu iniciar um **histórico novo**. O Git
local usa a branch `main`, acompanhando `origin/main` no repositório público
[Apethor/STT-widget](https://github.com/Apethor/STT-widget).
O primeiro commit de fontes é `7bc334c`. A autoria usa o alias público e o
e-mail `noreply` da conta GitHub, configurados só neste repo.

A preparação do repositório público foi concluída com o nome **STT-widget**,
escolhido pelo usuário. O nome foi aplicado à documentação, à identidade
visível, às traduções e aos scripts/launchers. Os próximos builds terão
`STT-widget.exe` para a interface e `stt-widget-cli.exe` para o terminal.
IDs e caminhos de dados/credenciais, módulo Go e ícones originais conservam
seus identificadores anteriores para preservar compatibilidade.
Há triagem inicial de informações sensíveis e reforço do `.gitignore`.
O usuário escolheu **MIT** para o código original; `LICENSE` foi adicionado
com o alias público como titular. Os dois áudios Nintendo foram substituídos
por **Impulso** e **Brilho**, efeitos originais sintetizados localmente e
dedicados ao domínio público via CC0, conforme a preferência do usuário.
O catálogo migra para versão 3, conservando IDs, nomes personalizados,
volume, posição e cópias já instaladas. Os áudios Nintendo não fazem parte
do histórico novo.
Consulte [REPOSITORIO-PUBLICO.md](docs/REPOSITORIO-PUBLICO.md) e **BL-101**
no [backlog](docs/BACKLOG.md). Foram publicados somente fontes, testes,
assets e documentação; não há executável ou pacote de release novo.

Nesta etapa, foram conferidos gofmt, sintaxe PowerShell, traduções,
regras de ignore, decodificação e reprodutibilidade dos hashes da síntese.
Foram acrescentados testes de migração dos catálogos 1/2; os testes Go
ainda não foram executados. A máquina tem Go compartilhado 1.26.2 e não
tem GCC no PATH; o projeto exige a toolchain fixada no README. Nenhuma
ferramenta foi instalada, executável gerado ou release criada.

## Estado herdado da máquina anterior

O aplicativo e os dados locais foram removidos a pedido do usuário, sem
backup. Não há executáveis, ZIPs, instalação, ferramentas locais nem dados
do aplicativo, então chamado AI Talker, em AppData. O launcher da raiz
também foi removido.
Perfis AWS, credenciais de outros programas e ferramentas compartilhadas
foram preservados; não havia chaves exclusivas do aplicativo na remoção.

Na limpeza seguinte, o usuário confirmou ficar com o **código Go e a
documentação essencial para gerar uma versão futura**, removendo o legado
Python. Artefatos, relatórios e oito checkouts antigos limpos foram
descartados; naquela máquina, o histórico Git e a árvore principal foram
preservados. O histórico não acompanhou esta transferência.
Não há referência Python, dependências ou testes Python na cópia de trabalho.
Fixtures usadas por Go, assets incorporados, traduções e licenças permanecem.

`cmd` deve ser mantida: contém as entradas do CLI e da interface.
`artifacts` não precisa ser conservada; `prepare-go.ps1` recria a pasta,
as ferramentas e o módulo auxiliar quando um novo preparo for autorizado.
Nenhuma ferramenta foi reinstalada e nenhuma versão nova foi compilada
ou publicada durante esta limpeza.

## Gerar uma versão futura

O procedimento está no [README](README.md). Preserve `cmd`, `internal`,
`scripts`, `tests/fixtures`, `go.mod`, `go.sum`, `scripts/toolchain.json`,
os recursos e as licenças.
Os helpers abrem os executáveis gerados em `bin`, sem buscar releases antigas.
O pacote portátil exige o ZIP e o SHA256 da release escolhida.

O código mantém os identificadores de versão existentes; esta limpeza
não define o número da próxima release. Antes de uma entrega, atualize os
identificadores indicados no README, valide os comportamentos atuais e
confira Sobre e a interface no executável realmente entregue.
Os metadados herdados não estão todos alinhados: o script de build e
`internal/version` usam 0.12.0, enquanto `FyneApp.toml` declara 0.5.1.
Esta limpeza não alterou esses identificadores.
Não reinstalar/recompilar automaticamente apenas ao retomar esta pasta.

## Próximo lote

O backlog ativo continua em [docs/BACKLOG.md](docs/BACKLOG.md):
**BL-081 a BL-097, BL-099 e BL-100**.
**BL-098 foi abandonado pelo usuário** e não deve ser pesquisado ou implementado.
Ainda não há número nem autorização de implementação para esse conjunto.

As decisões pendentes incluem editor/recorte de screenshots, layout do
painel Resposta e do gravador, seek antes de Play, fallback configurável,
Azure como provider/autenticação, TTS, modelos Transcribe, wake word,
bypass do pré-processamento e testes na cloud.
BL-087 exige pesquisa do código e alinhamento antes de escolher níveis,
retentativas ou prazos; seus exemplos não são decisões fechadas.
Azure real depende de recursos/permissões indisponíveis nesta máquina.
Não criar infraestrutura paga nem assumir acesso a esses serviços.

Capturas pessoais usadas nos relatos foram removidas na desinstalação.
As descrições úteis permanecem no backlog, sem depender desses arquivos.
O retorno positivo sobre a Conversa ao vivo não foi aceite integral do produto.

## Orientações de manutenção

[AGENTS.md](AGENTS.md) e os documentos técnicos de `docs` conservam os
contratos atuais. Relatos de releases, hashes de pacotes antigos e pesquisas
superadas não são dependências da geração e foram retirados da árvore.
O histórico da máquina anterior não está disponível nesta cópia.

Na máquina anterior, as 30 skills gerais de `samber/cc-skills-golang` foram
instaladas fora do projeto e não foram removidas. Sua disponibilidade nesta
máquina não foi verificada. Fonte/revisão e limites de uso estão em
[docs/GO-SKILLS.md](docs/GO-SKILLS.md). Não dependem dos artefatos descartados.
