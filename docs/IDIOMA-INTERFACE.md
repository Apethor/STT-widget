# Idioma da interface

A interface oferece **Português (Brasil)** e **Inglês** em
Configurações → Visual → Idioma da interface. A escolha aplica imediatamente
e persiste como `interface_language` no arquivo de preferências. Instalações
anteriores, sem o campo, continuam em português. A troca conserva a página
aberta, os rascunhos das configurações, a aba, os editores e as tarefas.

O idioma da interface é independente do idioma de reconhecimento e da
conversa. O modo usa os nomes **Conversa ao vivo /
Speech to speech**. O app traduz seu próprio texto: cabeçalhos, menus, ações, tooltips,
configurações, descrições, ajuda, placeholders, confirmações e erros próprios.
O catálogo está em `internal/locale/en.json`; `internal/locale` cuida das
mensagens exatas e formatadas, conservando os parâmetros. A interface usa
`uiText`/`uiTextf` e uma atualização no thread existente do Fyne que modifica
somente os campos de apresentação.

O cache remove referências a componentes descartados depois de percorrer as
janelas, páginas e abas ainda pertencentes ao aplicativo. Abas ocultas e a
sequência completa do Desfazer permanecem nesse conjunto. Os seletores
compartilhados usam chaves fracas, conservando seus valores de origem sem
manter uma configuração fechada na memória.

Textos digitados, transcrições, respostas dos serviços, instruções e prompts,
nomes personalizados, arquivos, caminhos e IDs de modelos não são traduzidos.
Entradas só têm seu placeholder traduzido. Seletores conservam a identidade
original de cada opção: por exemplo, `Média` e `Médio` podem aparecer como
`Medium`, sem confundir qualidade de vídeo com esforço de raciocínio.
Nomes de conexões, instruções, dispositivos e sons personalizados têm proteção
explícita. Mensagens brutas de provedores permanecem intactas, mesmo dentro de
um erro do app. Diálogos e botões pertencentes ao Windows seguem o idioma
configurado no sistema; o app traduz seus títulos e filtros próprios.

Novos grupos automáticos têm um marcador opcional
`group_automatic_names` no workspace. Somente um nome que ainda corresponda ao
marcador pode alternar entre `Grupo N` e `Group N`. Renomear remove o marcador.
Nomes antigos explícitos, mesmo se parecidos com `Grupo N`, são preservados
por serem ambíguos. Fechamento, desfazer, juntar, movimentar e restaurar mantêm
essa informação. Nomes automáticos de abas alternam entre `Aba N` e `Tab N`;
nomes editados permanecem.

Os recursos usam **Painel de sons / Soundboard**, **Gravações de som /
Audio recordings**, **Captura de tela / Screen capture** e **Captura de
vídeo / Video capture**. Os diretórios `soundboard`, `gravacoes`,
`screenshots` e `videos` conservam os nomes e caminhos.
