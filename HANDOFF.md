# Handoff — STT-widget

Atualizado em **15 de outubro de 2026**.
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

## STT Hub — organização separada (BL-103)

O usuário autorizou preparar o pivot web/SaaS em um projeto independente,
com o nome **STT Hub** desde o início. Público inicial: profissionais
individuais que querem transcrever áudio e trabalhar textos. O STT-widget
pode ser retomado e desenvolvido em paralelo; não foi encerrado ou removido.

A nova pasta irmã é **STT-Hub**, com Git próprio em `main` e repositório
público [Apethor/stt-hub](https://github.com/Apethor/stt-hub), conforme pedido
explícito mais recente do usuário.
Para continuar lá, ler o README, o AGENTS.md e o HANDOFF.md dessa pasta.
Eles registram decisões, propostas, origem, contratos e mapa de módulos/testes.
As referências à origem usam links do GitHub, sem depender desta pasta
local, e o novo README inclui instruções para clonar em outra máquina.
A referência de fontes deste desktop é o commit
`361788018f841637d09c3931f64e8b2819d16347`.

Foram preparados documentos e uma referência de prompt com o aviso MIT da
origem. Não foram copiados fontes Go, ferramentas, assets ou dados pessoais.
O STT Hub ainda não tem aplicação, stack, providers ou hospedagem definidos.
A próxima etapa é fechar o recorte e escolher a arquitetura antes do scaffold.
Identificadores legados deste desktop não devem ser levados ao projeto novo.

Esta organização não altera o estado funcional do desktop nem autoriza
implementar seu backlog pendente. Os detalhes estão em **BL-103**.

O clone anônimo do STT Hub passou, com os 12 arquivos e 28 links internos
conferidos sem depender da pasta de origem. Evidência local ignorada pelo
Git: `artifacts/validation/stt-hub-portability-a1bbc37ca083410fb6b18aef93af401e.json`.

## Retomada local — BL-102

O usuário escolheu preparar e validar a base antes de novas funcionalidades.
A frase sobre o nome anterior foi removida do README. Go **1.27.2**, GCC
**16.2.0** e FFmpeg **9.0.2** foram preparados em `artifacts/tools`, com
hashes fixados conferidos. `go-env.ps1` usa `artifacts/cache` para caches
de módulos/compilação e `artifacts/tmp/go-build` para intermediários.
Nenhuma instalação global foi alterada.

O carregamento dos scripts desta transferência estava bloqueado por
`RemoteSigned`. A execução inicial usou política só do processo; os seis
scripts revistos necessários ao fluxo foram depois desbloqueados nesta
cópia local. A política permanente do Windows continua `RemoteSigned`.

Foi corrigido o runner de testes: `Get-Command go` devolvia os caminhos do
Go local e do Go global, que eram convertidos para um único caminho inválido.
O runner agora usa o primeiro executável resolvido, o mesmo Go local do PATH.

**Validação automatizada passou:** `scripts/test-go.ps1 -Integration`,
incluindo gofmt, `go vet`, testes de módulos, detector de corridas,
**448 testes desktop em 23 lotes funcionais e 23 com detector de corridas**,
e integrações locais. A migração dos sons 1/2 para 3 passou também com
detector de corridas. Áudio físico opcional não foi habilitado.
Evidências locais: `artifacts/validation/go-race-ab1ef1cf820a4c4790a4771e90799ac4`.

Foi gerada uma compilação local em `bin`, com CLI/GUI renomeados, codecs e
avisos de licença. CLI `version` e `help` passaram. A conferência nativa
`smoke-desktop.ps1 -IntegratedFrame -Screenshot` passou na janela real:
título, ícones, barra integrada, arraste/cantos, minimizar/maximizar/restaurar,
segunda abertura na mesma instância e encerramento. A aparência da captura
e o caminho de erro do perfil AWS sintético foram conferidos.
Evidências locais: `artifacts/validation/desktop-017182be5670450da81731360f46faf0`.
Dados eram sintéticos/isolados; não houve chamada a serviço AWS real.

O metadado Fyne foi alinhado à base **0.12.0** já usada pelo build.
Não foi criada release, ZIP ou instalação do aplicativo. Esses resultados
não são aceite funcional do usuário nem validação nativa integral de uma
nova entrega. Sobre continua coberto pelo teste automatizado; a conferência
visual dessa página no executável de entrega permanece para a entrega futura.

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
O script de build e `FyneApp.toml` usam a base 0.12.0; `internal/version`
mantém 0.12.0-dev antes da injeção do build. Essa base herdada não define
o número da próxima release. Não reinstalar/recompilar automaticamente
apenas ao retomar esta pasta.

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
