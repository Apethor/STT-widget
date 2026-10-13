# Captura e edição de screenshots

A câmera fica no centro do rodapé principal. A captura
usa uma seleção independente do áudio e dos trabalhos de transcrição.
É possível usar a câmera durante uma gravação e abrir vários editores.

## Uso

1. Clique na câmera ou pressione Ctrl+Shift+S com a janela principal ativa.
2. Com o cursor em cruz, pressione o botão esquerdo e arraste a seleção.
   O preenchimento roxo é translúcido, com borda roxa. Solte para concluir.
   Esc cancela desde o início da captura, inclusive durante o arraste,
   restaura o ponteiro e mantém a gravação. Não abre editor nem salva imagem.
   Botão direito, perda de ativação ou mudança dos monitores também cancela.
   Um clique sem área permite tentar novamente.
3. A captura original recebe um nome como
   `Screenshot_2026-10-06_14-23-45.123000000.png`, com hora local.
   Ela é gravada antes de abrir o editor, em
   `%LOCALAPPDATA%\AI-Talker\screenshots\AAAA-MM-DD`, ou `screenshots\AAAA-MM-DD` dentro de
   `AI_TALKER_DATA_DIR`. Nomes repetidos recebem um sufixo sem sobrescrever.
4. Use pincel, borracha, cor, espessura ou texto. Texto oferece Sans,
   Sans negrito e Monoespaçada, tamanho, acentos e várias linhas.
   Clique na imagem para escolher a posição e digite no diálogo.
5. Salvar/Ctrl+S substitui o mesmo PNG com as marcações. A lixeira exclui
   somente o arquivo desse editor e fecha a janela. X mantém a última
   imagem salva e descarta alterações que ainda não foram salvas.
6. O botão de copiar, ao lado da lixeira, copia o caminho absoluto do PNG
   daquele editor. Não salva nem descarta marcações pendentes; funciona
   independentemente de outros editores de screenshot abertos.

A lupa e a lista de zoom ficam fixas à direita do cabeçalho,
inclusive em janela estreita. **Ajustar à janela**, **100%** e os percentuais
de **75% a 200%** mudam somente a visualização. As marcações continuam nas
coordenadas do PNG original; o zoom não salva, redimensiona nem altera seus
pixels. Quando a imagem ultrapassa a área disponível, use a rolagem.
Trocar o percentual atualiza imediatamente a imagem, sem precisar
redimensionar a janela.

Desfazer/Refazer e Ctrl+Z/Ctrl+Y operam sobre marcações completas, incluindo
texto e borracha. A borracha restaura a imagem original, sem pintar de branco.
A pasta do editor abre a subpasta do dia da imagem; o botão de pasta no rodapé
principal oferece Gravações, Screenshots e vídeos. Nenhuma captura é enviada a serviços de nuvem.
As subpastas diárias surgem automaticamente. Arquivos anteriores
e seus links continuam acessíveis na pasta original.

## Links da gravação

Uma seleção iniciada durante a gravação acompanha aquele áudio e sua aba,
mesmo quando a seleção termina depois da transcrição. Clicar
na câmera marca a posição no áudio em todos os modos. **Sem Stream**, o
reconhecimento acontece depois de parar; os links entram entre a fala anterior
e a seguinte, tanto no **Bedrock quanto no Whisper local**. Não há horários
visíveis nem lista de links no final das novas gravações.
A visualização de leitura troca somente a apresentação por links azuis
`Screenshot 1`, `Screenshot 2` etc.; clicar abre o visualizador padrão do
Windows. Copiar, salvar e persistir continuam usando os caminhos completos.
O botão de editar permite voltar ao texto original.

**Stream** marca a posição da screenshot ao iniciar a seleção,
fecha o trecho de áudio nesse instante e mostra o caminho durante a
gravação. O marcador tem horário relativo ao áudio. Um reconhecimento
atrasado é inserido antes/depois da imagem conforme a posição gravada.
Parar não repete esses trechos nem acrescenta uma lista final.
Copiar continua usando caminhos completos.

Trocar de aba, limpar o texto durante a captura ou retomar a transcrição
preserva os vínculos da gravação e não duplica os links. Excluir uma captura
pelo editor remove também seu link. Uma captura iniciada fora da gravação
não acrescenta uma lista. Descartar o áudio mantém os PNGs, sem acrescentar
links de uma gravação descartada. No Stream, descartar mantém o texto e os
links que já apareceram, encerrando as requisições ainda pendentes.

Excluir alcança também as referências guardadas no histórico de
áudio, Desfazer limpeza, abas fechadas e origens de Juntar/Mesclar.
Desfazer ou reabrir não restaura um link para o PNG excluído. As falas e os
pares de prompt/resposta já enviados permanecem preservados.
A exclusão também invalida a nota e o comentário de qualquer
avaliação de transcrição ligada à screenshot, incluindo os registros
internos de abas já fechadas.

## Implementação

`internal/platform/screenshot_windows.go` captura o desktop virtual com GDI
antes de abrir a sobreposição. A seleção usa uma janela Win32 sem moldura,
com cursor próprio e AlphaBlend para o preenchimento. O recorte usa os pixels
originais: a borda, o preenchimento e as instruções não aparecem no PNG.
Coordenadas incluem monitores à esquerda/acima da origem. A thread de seleção
usa consciência de DPI por monitor e restaura seu contexto ao terminar.

A thread nativa aguarda mensagens e um evento de cancelamento do contexto
com `MsgWaitForMultipleObjectsEx`. Fechar o aplicativo sinaliza esse evento.
A captura tem seu próprio contexto, independente da gravação.
O atalho Esc da janela principal cobre a preparação; a seleção nativa registra
Esc temporariamente, inclusive quando o Windows demora a lhe dar foco.
O atalho é removido ao terminar. Cancelar depois de criar o PNG, antes de abrir
o editor, remove somente esse arquivo e seu marcador.
As alocações e callbacks nativos não retêm ponteiros Go. GDI, janela e
evento são liberados ao concluir ou cancelar.

`internal/screenshot` mantém o original, as operações de edição e o raster
atual. Redimensionar a prévia não altera as coordenadas dos pixels. O editor
possui seu próprio arquivo e histórico. Salvar recebe um snapshot independente
e usa `store.Write` para substituição atômica. Durante salvar/excluir, o editor
bloqueia mudanças e a lixeira; fechar a janela durante um salvamento iniciado
permite que ele termine. Falhas mantêm o editor disponível para nova tentativa.

O editor nativo é uma janela pertencente à janela principal, permanecendo
acessível quando o aplicativo está fixado por cima das outras janelas.
`screenshot_links.go` mantém os vínculos por gravação, os metadados da sessão
e a remoção de referências. Somente os PNGs registrados na pasta screenshots
do aplicativo podem ser abertos como links locais.

`stream.go` separa a posição dos links da publicação durante a
captura. `transcribe_on_stop` no arquivo de linha do tempo permite screenshots
inline sem usar o transporte/prompt de Stream. Uma gravação comum ganha a
linha do tempo na primeira seleção; cancelar sua única seleção restaura o
reconhecimento comum do arquivo inteiro. Equipe mantém sempre suas duas trilhas.
As posições usam as amostras admitidas pelo gravador, excluindo pausas; o ponto
é reservado ao clicar na câmera, antes do tempo gasto arrastando a seleção.
Depois de parar, cópias de trechos PCM são cortadas nesses pontos e em partes
de até 60 segundos no Voxtral e 120 nos demais motores, respeitando limites
menores configurados. Não há sobreposição de áudio nos cortes offline.
Os WAVs originais permanecem completos. Cada trilha usa seu deslocamento
inicial, e o início lógico após um corte coincide com o marcador mesmo quando
o deslocamento fica entre duas amostras. A montagem usa posições, nunca a
ordem de chegada das respostas. Sem Stream, publica somente ao concluir.
Tentar novamente retoma partes pendentes; Retranscrever reconhece todas em
outro cache e preserva os marcadores. A sessão e o manifesto guardam esses
dados. Arquivos antigos sem marcadores temporais mantêm seus links existentes.

As fontes Go são incluídas por `golang.org/x/image`, já fixado em `go.mod`;
não há dependência nova nem ferramenta externa de edição. As licenças de
dependências continuam incluídas no pacote Windows.

## Conferência de manutenção

Use `scripts\test-go.ps1 -Integration` e `scripts\smoke-screenshot.ps1` depois
de preparar as ferramentas, com imagens sintéticas e dados isolados.
Confira pixels, coordenadas, Esc, seleção tardia, edição, salvamento,
exclusão e vínculos. DPI misto e conteúdo protegido por DRM precisam de
conferência no equipamento; testes simulados não comprovam esses casos.

## Referências oficiais

- [BitBlt](https://learn.microsoft.com/en-us/windows/win32/api/wingdi/nf-wingdi-bitblt).
- [MsgWaitForMultipleObjectsEx](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-msgwaitformultipleobjectsex).
- [SetThreadDpiAwarenessContext](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setthreaddpiawarenesscontext).
