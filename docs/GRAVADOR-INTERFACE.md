# Gravador, grupos e progresso

## Modos

O controle antes chamado Stream abre três escolhas por aba: **Gravação e
texto**, **Transcrição Stream** e **Conversa ao vivo**. A mesma escolha fica
em Configurações → Áudio. Selecionar um modo não inicia captura ou envio.
Abas novas começam em Gravação e texto.

Conversa ao vivo usa somente **microfone**, preservando as preferências de
Sistema/Equipe dos modos convencionais. Usa o perfil/região AWS e Nova 2.5
Sonic por padrão, com Nova 2 como alternativa. Ao iniciar, o serviço recebe
áudio continuamente e devolve respostas nas pausas; Encerrar conserva os
conteúdos recebidos. O painel Resposta substitui Processar texto enquanto
esse modo está selecionado, com histórico e reprodução próprios.
Ao sair do modo, o texto e a conversa de processamento reaparecem.
Detalhes de voz, saída, interrupção e limites em
[SPEECH-TO-SPEECH.md](SPEECH-TO-SPEECH.md).

Sem áudio, **Pronto para começar** aparece novamente em verde, na mesma
posição. Configurações → Visual permite trocar imediatamente entre
português e inglês sem mudar o idioma do reconhecimento.

## Altura, recolhimento e dispositivos

A **altura** escolhe Pequeno, Médio ou Grande. A largura alarga esse modo:
somente Pequeno usa uma linha quando ela couber. Estreitar aumenta até o
mínimo necessário; voltar a alargar conserva a altura escolhida. A divisória
muda o modo pela altura, e o botão percorre os três. No Pequeno, o nome e a
cópia do caminho precedem o relógio; no Médio, a mensagem sobe para o cabeçalho
quando couber entre nome e atalhos. O Grande conserva sua distribuição.

O botão **Recolher Gravação** deixa uma faixa com título/expandir e a indicação
de captura ativa. Durante a captura, há acesso a **Parar e transcrever**.
Reabrir restaura a altura, preservando áudio e tarefa. O estado pertence à
aba, persiste e é herdado por abas novas, como os outros tamanhos.

**Atualizar dispositivos** está no menu de fontes e em Configurações → Áudio.
Renova microfones e saídas sem substituir uma escolha que desapareceu:
mostra dispositivo indisponível e exige nova seleção antes de gravar.
Durante a captura, a atualização conserva as fontes em uso e bloqueia troca.
Configurações também escolhe a saída capturada como Sistema/loopback.

Grupo novo usa o **menor número livre**, mantendo nomes existentes; um nome
personalizado exatamente igual a “Grupo N” também ocupa esse número.
O título mostra somente STT-widget; versão/revisão ficam em Sobre.
O modal de Configurações acompanha a largura da janela em tempo real.

## Um menu por grupo

Cada linha mostra **+** para criar uma aba naquele grupo e **três pontinhos**
para abrir seu menu. O botão separado de grupos foi removido porque suas
ações eram um subconjunto do menu de três pontinhos.
Um **+ separado à esquerda de todas as linhas** cria um novo grupo no fim,
com nome próprio **Grupo N** e uma aba vazia selecionada. Esse botão é
distinto do + de cada linha, que cria somente uma aba naquele grupo.
O clique direito no nome abre o mesmo menu, incluindo a lista de abas
daquele grupo. Abrir o menu de outra linha não troca a aba selecionada.

| Ação | Resultado |
| --- | --- |
| Nome de uma aba | Seleciona aquela aba do grupo. |
| Renomear grupo… | Salva o nome da linha, preservando abas e textos. |
| Largura do nome… | Ajusta o limite do nome somente daquele grupo, de 40 a 320 pixels lógicos. |
| Fechar grupo… | Remove a linha e suas abas; se era o único grupo, cria um novo vazio. |
| Fechar abas deste grupo… | Mantém a linha, seu nome e a primeira aba com ambos os editores vazios. |
| Novo grupo | Acrescenta outra linha, começando em Aba 1. |
| Fechar abas de todos os grupos… | Após confirmação, mantém somente o primeiro grupo e sua primeira aba vazia. |

Fechar uma aba ou grupo vazio é direto. Texto na transcrição ou no resultado
pede confirmação. Limpar as abas do grupo ou de todos sempre pede confirmação.
Rascunhos e gravações continuam preservados; o grupo que contém a captura
em andamento não pode ser fechado ou limpo.

O nome usa somente sua largura natural, até o limite sugerido de **112
pixels lógicos**. Um nome maior termina com reticências.
Arraste a pequena alça vertical entre nome e abas para mudar o limite,
ou use o slider de **Largura do nome…**. O ajuste aparece durante o arraste
e é salvo ao concluir. Falha ao salvar restaura a escolha anterior.
Duplo clique na alça ou na bolinha do slider restaura 112.
Cada grupo guarda seu limite em `workspace-go-v1.json`; sessões antigas usam
o padrão. A janela estreita pode abreviar o nome antes desse limite para
preservar espaço para abas e ações.

A barra horizontal das abas mantém **3 pixels lógicos**, inclusive ao passar
o mouse, com fundo e sombra transparentes. Título e X usam menos
padding; a largura ajustável do grupo continua independente.

Arraste uma aba para mudar sua posição na linha ou movê-la para outro grupo.
O marcador mostra onde ela será inserida; a mudança é salva ao soltar dentro
de uma linha visível. Soltar fora da janela cancela o movimento.
Ao mover a última aba, o grupo vazio é removido se houver outro.
Os nomes dos grupos remanescentes e o conteúdo das abas são preservados.

**Desfazer fechamento** mantém uma pilha de abas fechadas por
**30 segundos**: cada novo fechamento de aba
ou grupo dentro do prazo renova o período das entradas
disponíveis. Fechar A, B e C permite recuperar C, B e A por cliques
sucessivos, com grupo, posição, textos, áudio, históricos, opções e tamanhos.
Um grupo removido ao fechar sua última aba é restaurado uma única vez; trabalho novo na substituta
automática é preservado.
Fechar grupo descarta as entradas recuperáveis desse grupo e renova o
prazo das entradas restantes de outros grupos.

A pilha e seu prazo absoluto ficam em um campo opcional do workspace,
para conservar a recuperação ao reabrir. O tempo continua correndo com o
app fechado; reabrir ou desfazer não renova o prazo.
Consulte [Desfazer persistido](DESFAZER-PERSISTIDO.md).

Juntar e Mesclar entram na mesma pilha como operações completas:
**Desfazer Juntar** ou **Desfazer Mesclar** recupera todas as origens,
inclusive o texto anterior da primeira aba, em um clique. Antes de substituir
a versão combinada, o app a preserva com as alterações posteriores em
`rascunhos/antes-de-desfazer-*.json`.
O workspace da restauração é salvo antes de publicar as abas e retirar a
entrada. Falha de preservação ou salvamento mantém o estado atual e a
recuperação dentro do prazo. A restauração não reinicia tarefas nem aceita
respostas antigas, e screenshots excluídas continuam excluídas.
Consulte [processamento e preservação](PROCESSAMENTO-TEXTO.md).

## Nome e caminho da gravação

O rótulo do áudio carregado ou sendo gravado mostra **HH:mm · dd/MM**
quando reconhece a data/hora no nome, por exemplo **16:00 · 08/10**.
Arquivos com outros nomes continuam mostrando seu nome real.
O tooltip conserva o caminho completo. **Copiar caminho** fica à esquerda
do rótulo; copia o caminho absoluto do arquivo, inclusive durante a captura.
Em Equipe, copia os dois WAVs, um por linha, inclusive para uma gravação
antiga com manifesto.

**Novo áudio** aparece no rótulo somente quando não há arquivo. Seu botão
de atalho usa o ícone **+** e prepara outra gravação, preservando a conversa
ativa para Reprocessar; as últimas respostas editadas continuam no contexto.
Novas gravações usam
`AAAAMMDD-HHMMSS.wav`; Equipe acrescenta `-mic` e `-sis`. Uma colisão recebe
`-2`, `-3` etc., sem substituir uma gravação existente.
O rótulo legível não renomeia os arquivos. Importados/antigos conservam
seus nomes e caminhos.

## Altura e volumes

Na disposição **Uma coluna**, a divisória logo abaixo do gravador muda sua
altura. Ao ultrapassar a altura natural de **Grande**, o conteúdo usa essa
disposição. Alturas intermediárias usam **Médio** e a menor usa **Pequeno**;
o ícone acompanha o modo. A largura pode exigir temporariamente uma altura
mínima maior, sem mudar o modo escolhido.

O modo é salvo ao soltar a divisória. Se o salvamento falhar, preferência
e ícone voltam ao valor anterior, com erro recuperável.
O botão de tamanho percorre os três modos e escolhe sua altura natural.
A altura total e a rolagem da coluna permanecem; a divisão entre transcrição
e processamento continua independente. A alça abaixo dos editores
ajusta a altura total da coluna.
Se Grande não couber ao usar Ampliar, a coluna cresce o suficiente e permite
rolagem, preservando o tamanho da janela.

Dentro do espaço disponível, relógio e gráfico aumentam com a altura.
O relógio respeita a largura para manter os controles acessíveis.
Uma janela que encolhe usa a disposição compacta quando Grande deixa de caber.
**Pequeno** usa uma linha com largura suficiente; numa janela estreita,
conserva o modo e aumenta até o mínimo do layout empilhado.
**Médio** mantém relógio e onda lado a lado; sua mensagem sobe ao cabeçalho
quando couber entre nome e atalhos. **Grande** distribui relógio, mensagem
e onda verticalmente. Médio/Grande mantêm os controles na linha inferior.
O botão **Parar e transcrever** volta a
acompanhar a largura depois de trocar de layout e retornar à coluna.

**Transcrição** e **Processar texto** recolhem independentemente nos três
layouts. A coluna usa faixas horizontais e as demais disposições usam
faixas verticais; ambos podem ficar recolhidos sem cancelar tarefas.
Em Configurações → Visual, **Inverter transcrição e processamento**
troca sua ordem, preservando conteúdo e recolhimento.

Os tamanhos dos painéis pertencem à aba. A divisão dos editores, a divisão
do gravador e a altura total da coluna persistem ao reabrir e são recuperadas
ao selecionar ou desfazer o fechamento da aba. Uma nova aba, inclusive
a primeira de outro grupo, herda os tamanhos da última aba ativa; depois
os ajustes são independentes. Sem aba anterior, usa os padrões.
Recolher preserva o tamanho expandido; as novas abas começam expandidas.

Um único **botão arredondado de volume** abre o popup **Volumes da captura**.
O botão fica na linha de atalhos, entre **Microfone** e **Stream**, tanto em
**Médio** como em **Grande**. Os sliders aparecem somente ao clicar.
O popup mostra Microfone, Sistema ou ambos em Equipe, conforme as fontes
em uso; durante a gravação, acompanha a escolha fixada no início da captura.
As barras usam trilhos finos, bolinhas e porcentagens alinhadas.

Ganhos de captura continuam de **0 a 200%**, independentes por fonte.
O arraste aplica o ganho durante a gravação; soltar salva. Uma falha restaura
o valor anterior também no PCM. Dispositivos e fontes em uso permanecem
congelados para a captura; ajustar tamanho, tema ou ganho não pausa o áudio.
Duplo clique **na bolinha** restaura 100%, aplicando e salvando pelo mesmo
fluxo de um ajuste normal. Duplo clique em outra parte do slider mantém o valor.

A reprodução mantém seu próprio botão e slider de **0 a 200%**, independente
dos ganhos. Acima de 100%, o limitador suave atua somente na cópia de reprodução;
os WAVs originais são preservados. Duplo clique na bolinha também restaura 100%.
O botão **Histórico de áudios desta aba** abre as fontes anteriores;
escolher restaura a transcrição acumulada correspondente sem transcrever
automaticamente. Consulte [reprodução e histórico](REPRODUCAO-AUDIO.md).

## Progresso de fontes e trechos

O gravador mostra **Trechos: N de M concluídos** para arquivos e fonte única.
Em **Equipe**, quando há somente uma faixa por fonte, usa
**Fontes: N de M concluídas**.
Por exemplo, uma gravação de cerca de seis segundos com microfone e sistema,
sem Stream nem cortes, tem duas faixas: uma de cada fonte.
**Fontes: 1 de 2 concluídas** indica que uma fonte terminou; a outra ainda
aguarda reconhecimento. Cada faixa cobre o áudio completo daquela fonte.

Equipe com várias faixas por fonte usa **Trechos: N de M concluídos**.
O rodapé detalha **Microfone x/y · Sistema x/y**, a porcentagem e quantas
faixas estão em andamento, com erro ou para conferir.
Screenshots ficam fora da contagem.

A porcentagem da linha do tempo usa duração, incluindo o progresso interno
das partes em andamento. Um trecho com erro não soma duração concluída.
Ao retomar, os aceitos continuam contados e só os pendentes voltam à fila.
O status de retentativa mantém a contagem; esperar a AWS por mais de dez
segundos também mantém os números no gravador.

Sem Stream, o texto anterior permanece enquanto as partes são reconhecidas.
Se a tentativa terminar com falhas, o texto concluído é apresentado como
parcial e as pendências ficam disponíveis para retomar.
No Stream, a contagem aparece na finalização dos trechos restantes após parar.
Progresso e respostas pertencem à aba e geração de origem; trocar de aba
não redireciona uma tarefa. Cancelar continua liberando os controles prontamente.

Consulte [a linha do tempo e os limites de áudio](STREAM-EQUIPE-CONTEXTO.md)
e [as retentativas do Voxtral](RETENTATIVAS-TRANSCRICAO.md).

## Motor, modelo e Amazon Transcribe

**Configurações → Áudio → Transcrever com** escolhe **AWS Cloud**,
**Whisper local** ou **API**. As barras de qualidade na Transcrição
abrem a escolha contextual:
na AWS, gravação completa quando Stream está desligado e modelo de Stream
quando está ligado; no Whisper, o catálogo de modelos locais. Na API,
abrem sua configuração de áudio. Um modelo Whisper ausente precisa ser
instalado nas configurações; **Arquivo local / configurar…** também permite
manter um arquivo GGML personalizado. Whisper e API desabilitam Stream.
As preferências AWS de gravação completa e Stream permanecem independentes.
Selecionar o motor de áudio não altera o provider/modelo de processamento
de texto, configurados em LLM.
Os nomes completos e os requisitos ficam em Configurações → Áudio → AWS Cloud;
[APIs e perfis](APIS-PROVIDERS.md) descreve a alternativa com chave própria.

**Transcribe · arquivo (S3)** precisa
de um bucket existente na região selecionada.
Ao escolher esse modo sem bucket, o app oferece **Configurar bucket S3**
ou **Usar Transcribe sem S3**. A primeira opção abre Conexões → AWS no campo correto;
a segunda escolhe explicitamente **Transcribe · streaming (sem S3)**.

Para um áudio já carregado, a alternativa pode **tentar novamente** naquela
aba, preservando arquivo, transcrição anterior e resultado processado.
Salvar a configuração de arquivo com bucket vazio informa o requisito.
A preferência de Stream continua independente; não há troca silenciosa de motor.
AWS ainda exige as permissões do serviço escolhido.
Consulte [o catálogo e os requisitos](MODELOS-CLOUD.md).

## Logo junto do título

O logo junto do título usa um PNG gerado do SVG original no tamanho nativo,
com cores por paleta: azul `#315fc4` no tema Claro, escuro `#2a3443` no Escuro
e verde `#0f6b50` no Verde.
O ícone do executável e da barra de tarefas mantém a arte original.
