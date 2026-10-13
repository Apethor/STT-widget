# Recuperação de transcrição

## Partes menores e retomada

Voxtral, sem Stream, usa faixas de até **60 segundos** na linha do tempo
de Equipe e de screenshots. Whisper e os outros motores mantêm o limite
de até 120 segundos, respeitando partes menores nas configurações.
Importações comuns mantêm a duração configurada e o cache existente.

Retomar uma sessão antiga conserva as faixas concluídas, seus IDs e textos.
Somente as faixas pendentes maiores que o limite atual são repartidas,
usando índices exatos de amostras. Os cortes das screenshots e deslocamentos
entre fontes ficam preservados. Não há nova gravação nem alteração dos WAVs
originais. O resultado offline entra no editor quando a tentativa termina;
havendo erros, o texto concluído aparece como parcial e mantém as pendências.
Retranscrever continua sendo a ação que força reconhecimento de todas as faixas.

## Retomar enquanto a tentativa anterior termina

Uma retomada com o mesmo cache mostra **“Aguardando a tentativa
anterior concluir e salvar os trechos…”**. Espera a liberação do lock antes
de ler ou atualizar resultados. Não envia outra chamada enquanto espera.
Partes concluídas pela tentativa anterior são reutilizadas; somente as
ausentes, com erro ou para conferir são reenviadas depois.

Essa espera respeita **Cancelar**, o prazo do chamador e o encerramento do
serviço. Cancelar a espera preserva o lock e as partes da tentativa anterior;
não apaga nem força a liberação de um cache que pertence a outro pedido.
Arquivos de áudio permanecem intactos. Mudar modelo ou configurações continua
separando o cache, conforme o conteúdo do arquivo e a configuração.

O [histórico de áudio da aba](REPRODUCAO-AUDIO.md) também restaura a tentativa
e as pendências correspondentes, para retomar um áudio anterior sem misturar
suas partes com as gravações seguintes.

## Tentativas e prazos

O serviço controla até **três tentativas de transcrição Voxtral** para erros
temporários: tempo limite, limitação de chamadas, indisponibilidade da AWS,
erro transitório do modelo ou interrupção de conexão/resposta.
Cada tentativa recebe um contexto novo. O prazo por chamada é
`max(120, min(360, 2 × duração + 30))` segundos; o serviço permite pelo menos
150 segundos quando não recebeu um prazo explícito.
Um prazo menor fornecido pelo chamador continua prevalecendo.
O cliente HTTP também permite o prazo da parte configurada, evitando cortar
a chamada antes do contexto do reconhecimento.

As esperas usam jitter: 0,5–1 segundo antes da segunda tentativa e
1–2 segundos antes da terceira. As tentativas mantêm o limite compartilhado
de **duas chamadas simultâneas**, ajustável de **uma a quatro** em
Configurações → Áudio. Esperas e recuperação local liberam essa vaga.
O status informa a nova tentativa;
o erro final identifica a parte e a quantidade de tentativas.
O SDK não acrescenta retentativas ocultas às operações Converse dessa
transcrição. Processamento de texto, STS e os adaptadores Transcribe/Sonic
mantêm suas políticas próprias.

As correções semânticas existentes são distintas: uma resposta truncada
pode ter uma chamada adicional com orçamento maior, e fala curta sem texto
pode receber um segundo pedido. Permanecem limitadas e não tornam uma
resposta incompleta uma parte concluída.

Login expirado, falha do processo de credenciais, certificado não confiável,
permissão negada, pedido/modelo/região inválidos e limite de saída esgotado
não provocam retentativas automáticas de transporte.
Mensagens e logs preservam a causa internamente sem copiar diagnósticos
brutos de credenciais. O log de retentativa contém somente número da parte,
tentativa, limite e duração.

Cancelar libera a interface imediatamente. Uma chamada Converse já enviada
pode concluir e salvar sua parte no cache, como nas versões anteriores.
Cancelar impede novos pedidos e interrompe a espera entre tentativas.
Fechar o serviço encerra também chamadas em andamento.

Somente partes concluídas e sem indicação de revisão são reutilizadas.
O botão Tentar novamente continua retomando partes ausentes ou marcadas,
inclusive após reabrir o programa. Falas já aceitas da linha do tempo ficam
preservadas; a mudança de limites não reenfileira essas faixas.

## Limite de saída e recuperação local

Sem Stream, uma parte Voxtral que continuar atingindo o limite de saída é
subdividida automaticamente. Somente essa parte é reenviada; partes aceitas
permanecem. Os cortes usam índices PCM exatos, sobreposição limitada e
deduplicação conservadora, mantendo cobertura até a última amostra.

Cada parte original admite no máximo **12 envios físicos Cloud**, incluindo
retentativas de transporte e correções semânticas. Há até três níveis de
subdivisão, quatro contando a raiz; não subdivide uma parte de oito segundos
ou menos. A recuperação
da parte inteira tem prazo de 15 minutos, subordinado ao prazo/cancelamento
do chamador. O plano e os filhos aceitos ficam no cache; retomar reutiliza
esses filhos e tenta somente pendências.

No **Voxtral Small 24B**, falha de reconhecimento ou erro persistente de
limite/conexão pode usar o **Whisper já configurado**, uma vez naquela
pendência. O padrão permite esse fallback, com opção de desligar em Áudio.
Não instala/download modelos, não troca a preferência do motor e não se
aplica a Mini, API ou Stream. A inferência local tem prazo de até cinco
minutos e uma vaga de CPU compartilhada, liberando a vaga Cloud.
Preparação local e fila permanecem dentro do orçamento global de 15 minutos.

Uma resposta formada somente por marcadores como `[inaudível]`
ou `(inaudível)` também exige revisão quando houve fala detectada e participa
desse fallback. Palavras reconhecidas junto de um marcador permanecem texto
parcial, sem serem descartadas por essa regra. A retomada reconhece também
marcadores em caches antigos que ainda não tinham `needs_review`.
O cache/resultado guarda a tentativa local como indisponível, com erro ou
concluída; o diagnóstico permite conferir o motor usado e uma falha do Whisper.
Isso não altera o motor preferido, as partes já aceitas, o opt-out ou os
limites de Stream e cancelamento.

Silêncio, Cancelar, login/permissão/configuração inválidos e falha de escrita
não provocam fallback. O resultado registra o motor realmente usado por
parte/filho; o diagnóstico do progresso permite conferir sua origem.
Se a recuperação não concluir, mostra o texto pronto como parcial, conserva
edições e permite Tentar novamente após reabrir.
Falha ao escrever o cache mantém a parte marcada, sem contá-la como concluída.

Cancelamento impede inclusive chamadas semânticas internas novas. Uma chamada
Cloud que já tinha sido enviada ainda pode terminar de gravar o cache;
o cancelamento libera a interface e impede outra recuperação. Os locks de
outros pedidos continuam preservados.

Não há normalização especial para voz baixa. O fallback local usa uma cópia
do áudio original. Disponibilidade e precisão dos serviços continuam exigindo
conferência; as retentativas não garantem uma transcrição exata.
