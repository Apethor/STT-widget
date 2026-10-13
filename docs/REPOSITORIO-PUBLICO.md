# Preparação do repositório público

Registro: **13 de outubro de 2026**. Nome escolhido: **STT-widget**,
anteriormente AI Talker.

Repositório público: [Apethor/STT-widget](https://github.com/Apethor/STT-widget).
Foram publicados somente fontes, testes, assets e documentação.

## Git local

A transferência trouxe os fontes sem `.git`. O usuário escolheu começar
um histórico novo. A branch `main` acompanha `origin/main`; o primeiro
commit de fontes é `7bc334c`. A sessão GitHub CLI já estava autenticada.

A autoria foi configurada localmente com o alias público e o e-mail
`noreply` da conta autenticada, sem alterar a configuração global de outros
projetos. O remoto usa HTTPS e o helper do GitHub CLI configurado somente
neste repo, sem armazenar tokens nos fontes ou no arquivo de configuração
do Git. A visibilidade pública e o commit remoto foram conferidos pela API.

## Identidade do produto

O nome STT-widget foi aplicado à documentação, à identidade visível e às
traduções. Os scripts/launchers foram atualizados para produzir e abrir
`STT-widget.exe` (interface) e `stt-widget-cli.exe` (terminal), com nomes
distintos também em sistemas de arquivos Windows sem distinção de caixa.
Os pacotes futuros usam o prefixo `STT-widget-`.

Para preservar compatibilidade, caminhos de dados, variáveis
`AI_TALKER_*`, ID Fyne e referências de credenciais conservam seus nomes
anteriores. O módulo Go `ai-talker`, as entradas em `cmd` e os nomes dos
ícones originais também permanecem internos. Nenhum build foi executado;
a versão final ainda exige validação funcional e nativa na entrega futura.

## Triagem inicial de conteúdo

Foram inventariados 534 arquivos anteriores a este documento: fontes Go/C,
scripts, documentação, traduções, ícones e áudios de teste/efeitos.
Não foram encontrados executáveis, pacotes, logs, arquivos `.env`, bancos
de dados ou configurações pessoais persistidas nesta árvore.

Busca por padrões comuns de chaves AWS, tokens GitHub, chaves de API e
chaves privadas não encontrou credenciais reais. Os valores de credenciais,
identidades AWS e URLs com senha encontrados nos testes são sintéticos.
Os READMEs das fixtures documentam a geração de fala sintética.
Esta triagem não garante a ausência de todo segredo ou dado pessoal;
a revisão considerou também os 538 arquivos do primeiro commit, após as
alterações desta etapa. O índice não continha dados locais, ferramentas,
pacotes, áudios Nintendo ou padrões comuns de credenciais reais.

O `.gitignore` protege dados locais, exportações, artefatos, backups e
arquivos de credenciais. Assets em `internal` e fixtures em `tests` continuam
incluídos. Ao usar `AI_TALKER_DATA_DIR`, prefira um diretório fora do repo
ou uma pasta já ignorada, como `dados/` ou `artifacts/`.

## Decisões aplicadas na publicação

- Destino: `Apethor/STT-widget`, público, na conta já autenticada.
- A licença **MIT** foi escolhida para o código original e adicionada em
  [LICENSE](../LICENSE), usando o alias público como titular. Os scripts
  incluem esse aviso nos pacotes futuros. Conservar as licenças e os avisos
  próprios dos componentes de terceiros.
- O usuário autorizou substituir os dois excertos Nintendo por efeitos
  livres. Foram criados `impulse.wav` (Impulso) e `sparkle.wav` (Brilho),
  dedicados ao domínio público via CC0. Os arquivos Nintendo foram removidos
  antes do primeiro commit. O catálogo migra de versões 1/2 para 3, mantendo
  IDs, nomes pessoais, volume, posição e cópias instaladas. Procedência e
  hashes estão em [SOURCES.md](../internal/soundboard/assets/SOURCES.md).
- O conjunto do primeiro commit e a identidade efetiva de autor/committer
  foram revisados antes da publicação. Os áudios substituídos nunca foram
  comitados neste histórico novo.

## Verificação desta etapa

Foram conferidos gofmt, sintaxe dos scripts PowerShell, JSON de traduções
e seus parâmetros, regras de ignore para dados/segredos e conservação
dos assets necessários. Os novos WAVs foram decodificados com os codecs
compartilhados já disponíveis e reproduzidos numa geração independente
com hashes idênticos. Nenhum áudio foi reproduzido em dispositivo físico.

Os testes de migração foram adicionados, mas os testes Go ainda não foram
executados: a máquina tem Go compartilhado 1.26.2 e não tem GCC no PATH;
o projeto exige a toolchain fixada no README. Essas conferências não são
aceite funcional nem validação do aplicativo nativo.

O pedido está em **BL-101** de [BACKLOG.md](BACKLOG.md). O escopo inclui
nome, licença, proteção dos fontes e substituição dos dois efeitos. Não
inclui instalação da toolchain, build do aplicativo, pacotes ou implementação
do backlog de funcionalidades anterior.
