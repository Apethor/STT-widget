# Preparação do repositório público

Registro: **13 de outubro de 2026**. Nome escolhido: **STT-widget**,
anteriormente AI Talker.

## Git local

A transferência trouxe os fontes sem `.git`. O usuário escolheu começar
um histórico novo. O repositório foi inicializado em `main`, ainda sem
commits e sem remoto. A sessão GitHub CLI já estava autenticada.

A autoria foi configurada localmente com o alias público e o e-mail
`noreply` da conta autenticada, sem alterar a configuração global de outros
projetos. A sessão atual é `Apethor`; a consulta a `Apethor/STT-widget`
retornou HTTP 404, sem repo existente visível a essa sessão.

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
a revisão deve considerar o conjunto exato do primeiro commit.

O `.gitignore` protege dados locais, exportações, artefatos, backups e
arquivos de credenciais. Assets em `internal` e fixtures em `tests` continuam
incluídos. Ao usar `AI_TALKER_DATA_DIR`, prefira um diretório fora do repo
ou uma pasta já ignorada, como `dados/` ou `artifacts/`.

## Decisões antes da publicação

- Confirmar a conta/organização GitHub de destino para `STT-widget`.
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
- Revisar os arquivos e a identidade de autoria do primeiro commit; depois,
  configurar o remoto e publicar o conjunto revisado.

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
