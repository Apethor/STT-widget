# Painel de sons / Soundboard

O catálogo contém Aplausos, Risadas, Impulso, Brilho,
Ba dum tss e Uau!, além dos sons personalizados. A risada não reutiliza o
início com fala da amostra anterior. Impulso é uma varredura ascendente de
0,68 s; Brilho combina três sinos em 1,25 s, sem repetição automática.
Origem, condições declaradas e hashes estão em
`internal/soundboard/assets/SOURCES.md`. Os dois efeitos são sínteses
originais, disponibilizadas em domínio público via CC0. Substituem os
excertos Nintendo, que não fazem parte dos fontes ou pacotes novos.

## Reprodução e preparação

O motor usa uma saída WASAPI compartilhada
persistente, criada em uma thread própria, com eventos do dispositivo e
agendamento MMCSS Audio. O áudio dos efeitos é convertido uma vez para
PCM estéreo de 48 kHz ao abrir o aplicativo; importações são preparadas ao
serem adicionadas. Até ficar pronto, o controle indica preparação e os
botões indisponíveis permanecem desabilitados.
Um arquivo personalizado inválido fica identificado no aviso e não bloqueia
os outros efeitos ou uma importação válida posterior. O app conserva esse
arquivo e seu registro para que a pessoa decida renomeá-lo ou removê-lo.

Os efeitos preparados ficam em memória, com limite total de 128 MB. A cópia
de reprodução remove o silêncio inicial até o primeiro sinal acima de
aproximadamente −60 dB, mantendo até 2 ms anteriores; arquivos originais
continuam intactos. Quando há corte, o trecho é copiado para um array compacto:
o cache não conserva a alocação inteira do silêncio descartado.
Volume próprio de 0–100% é aplicado ao PCM, sem mudar
o volume da sessão CoreAudio e sem interferir no player do gravador.

Outro clique substitui o som; o mesmo botão reinicia. Não há fila de
cliques nem mistura de efeitos. A pequena parcela já enviada ao buffer
do dispositivo termina antes da parcela do novo efeito. Parar esvazia o
efeito ativo. O motor mantém o dispositivo preparado quando o painel
fecha e libera sua thread, COM, buffers e prioridade MMCSS ao encerrar
o aplicativo.

## Janela e dados existentes

A janela nasce sem moldura nativa, usa a barra integrada e permite arrastar,
redimensionar, minimizar, maximizar/restaurar e fechar pelos controles do
canvas. A matriz adapta o número de colunas à largura e pode rolar quando
há mais efeitos. Com a largura inicial, os seis efeitos usam três colunas;
3×3 é uma referência de organização, sem limite de nove botões.
Os atributos opcionais de cor DWM não impedem a instalação da moldura
e a restauração da posição em builds do Windows 10 que não os suportam.

A posição física da janela normal fica no próprio `soundboard.json`,
incluindo coordenadas negativas de outros monitores. A posição maximizada/
minimizada não substitui essa referência. Se o monitor foi desconectado,
a reabertura traz a janela para a área de trabalho do monitor mais próximo.

O manifesto migra de versões 1 e 2 para 3, mantendo volume, ordem, sons
personalizados e nomes editados. A risada usa um novo nome de arquivo;
a antiga cópia instalada permanece. Os quatro efeitos novos são
acrescentados sem renomear um som personalizado que já tenha o mesmo nome.
Impulso e Brilho usam novos arquivos, conservando os IDs anteriores dos
dois efeitos substituídos. Cópias instaladas antigas permanecem intactas.
Só nomes padrão não editados acompanham a substituição; nomes pessoais
continuam exatos. Um conflito com um nome já salvo recebe sufixo no nome
do efeito padrão, preservando o botão pessoal.
Nomes padrão acompanham o idioma; uma renomeação explícita ganha marca
persistida e conserva o texto escolhido pela pessoa.

A saída de áudio é escolhida ao abrir o aplicativo. Para mudar o dispositivo
usado pelo Painel de sons, encerre e abra o app novamente. A atualização dos
dispositivos do gravador não troca essa saída já preparada.
