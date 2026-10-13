# Captura de vídeo

O botão de vídeo no rodapé prepara uma área da tela para a
próxima gravação. Depois da seleção, iniciar o gravador captura áudio e vídeo
juntos. Pausar e retomar acompanham os dois; ao parar, o MP4 recebe o áudio
dos WAVs preservados. Em Equipe, as fontes continuam em WAVs separados.
O vídeo salvo permanece vinculado ao histórico de áudio e pode ser aberto
pelo botão da gravação.

## Pastas

O menu de Abrir pasta oferece **Capturas de vídeo**. Novas capturas ficam em
`videos/AAAA-MM-DD` dentro da pasta de dados do aplicativo, com nome derivado
do áudio e sufixo `-video.mp4`. Colisões recebem um número, sem sobrescrever
arquivos existentes. Caminhos e vídeos anteriores são preservados.

## Qualidade

**Configurações → Vídeo** oferece três presets. **Média** é o padrão também
ao carregar preferências anteriores que ainda não tinham essa opção.
A escolha é geral e fica definida ao começar cada gravação. Mudar a
preferência durante a captura vale para o próximo vídeo; pausar e retomar
conservam a qualidade fixada.

| Preset | Resolução | Quadros por segundo | Compressão H.264 |
| --- | --- | --- | --- |
| Baixa | Até 1280 pixels no lado maior | 15 | CRF 30 |
| Média | Até 1920 pixels no lado maior | 30 | CRF 23 |
| Alta | Resolução da área selecionada | 30 | CRF 18 |

Áreas menores não são ampliadas. A escala usa Lanczos e preserva a proporção
de exibição, inclusive quando as dimensões pares exigidas pelo H.264 resultam
em uma razão de pixels diferente de 1. O tamanho do arquivo também depende
do conteúdo e da duração; o preset não promete um tamanho fixo.

Os segmentos e o alinhamento com o áudio usam a taxa e a compressão do mesmo
preset. O áudio final usa AAC a 192 kb/s; os WAVs originais não são alterados.

## Indicação da área

Durante a captura, uma borda **roxa de um pixel lógico** indica a área.
Na pausa ela fica **cinza**; ao retomar volta ao roxo. A espessura física
acompanha o DPI efetivo do monitor, com arredondamento Win32 e mínimo de um
pixel. A janela não recebe cliques, não toma o foco e não aparece na barra de
tarefas.

A borda normalmente fica do lado externo do recorte. Nas extremidades
do monitor, ela é ajustada para manter a indicação visível, inclusive em
tela inteira. O Windows exclui essa janela da captura por
`WDA_EXCLUDEFROMCAPTURE`. Para os lados que precisam entrar no recorte, o app
exige Windows 10 2004 ou mais recente e a configuração de exclusão bem-sucedida.
Se não for possível preparar a borda, o vídeo falha de forma recuperável e
o áudio continua sendo gravado.

Parar, descartar, cancelar ou encerrar a captura remove a janela nativa.
Falhas de início, pausa, retomada, finalização ou saída do codificador também
removem a indicação.

## Fontes oficiais consultadas

- Microsoft, exclusão de janelas da captura:
  `https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwindowdisplayaffinity`
- Microsoft, janelas em camada e passagem de eventos do mouse:
  `https://learn.microsoft.com/en-us/windows/win32/winmsg/window-features`
- Microsoft, DPI da janela:
  `https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getdpiforwindow`
- Microsoft, arredondamento de escala:
  `https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-muldiv`
- FFmpeg, gdigrab:
  `https://ffmpeg.org/ffmpeg-devices.html#gdigrab`
- FFmpeg, escala e proporção:
  `https://ffmpeg.org/ffmpeg-filters.html#scale`
- FFmpeg, libx264/CRF:
  `https://ffmpeg.org/ffmpeg-codecs.html#libx264_002c-libx264rgb`
