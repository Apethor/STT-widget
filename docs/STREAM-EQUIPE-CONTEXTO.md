# Stream, equipe e conversa por aba

Equipe usa microfone + áudio do sistema em trilhas separadas.
Reprocessar continua a conversa daquela aba.

## Contratos de produto

| Melhoria | Comportamento e critério de aceitação |
| --- | --- |
| Stream | Mostrar texto automaticamente enquanto a captura continua, sem horários. Disponível na AWS Cloud; desabilitado no Whisper local. Pausar exclui o intervalo, parar termina os trechos pendentes, cancelar reconhecimento mantém o áudio. Resultados pertencem ao áudio e à aba de origem. |
| Screenshot no stream | Mostrar o link durante a gravação no instante da fala correspondente. A posição é marcada ao iniciar a captura da tela; um reconhecimento atrasado entra antes/depois do marcador conforme a ordem do áudio. Não agrupar essas imagens no rodapé final. Copiar mantém caminho completo. |
| Screenshot sem stream | Marcar a posição ao clicar na câmera, descontando pausas; reconhecer depois de parar e montar fala anterior, link, fala seguinte. Usar AWS Cloud ou Whisper, mantendo WAVs completos, aba de origem e posição ao retomar/retranscrever. Não publicar partes enquanto grava nem acrescentar lista final às novas gravações. |
| Cancelar screenshot | Esc cancela desde o início da captura, antes ou durante o arraste, restaura o ponteiro e preserva a gravação. Não abrir editor, salvar imagem nem manter marcador de uma captura cancelada. |
| Dispositivo no gravador | Somente o ícone de microfone fica no gravador. Clicar abre um menu compacto com Microfone, Sistema, Equipe e os dispositivos de entrada/saída, sem abrir Configurações. Escolher persiste imediatamente; clicar fora fecha. A captura atual mantém seus dispositivos até terminar. |
| Compartilhamento | Usar WASAPI em modo compartilhado e testar dois clientes simultâneos. Preservar identificação de microfones das preferências WinMM. Informar falha de acesso quando outro aplicativo mantém uso exclusivo, sem alterar preferências globais do Windows. |
| Equipe/tutorial | Oferecer Microfone, Áudio do sistema e Equipe em trilhas separadas. Mostrar a fonte e, quando confiável, a identificação de pessoas; não presumir que microfone signifique Você. Sem Stream, publicar o texto completo após terminar. |
| Volume | Um botão arredondado abre os ganhos de 0 a 200% conforme as fontes em uso: microfone, sistema ou ambos em Equipe. Aplicar ao PCM salvo e ao medidor durante a captura, sem alterar volumes globais. Persistir ao concluir o ajuste, restaurar o valor anterior em caso de falha e voltar a 100% por duplo clique na bolinha. |
| Reprocessar | Enviar o novo texto do box Transcrição como próximo pedido, com pedidos e respostas anteriores da própria aba. Processar inicia uma conversa nova. Erro, cancelamento e resposta rejeitada não acrescentam turnos. Edições na última resposta entram na continuação. |

**Configurações → Áudio → AWS Cloud** oferece **seis opções para gravação
completa**: Voxtral Small 24B, Voxtral Mini 3B, Amazon Transcribe · arquivo,
Amazon Transcribe · streaming e Nova 2 / Nova 2.5 Sonic · ASR experimentais.
**Stream tem cinco opções**, excluindo Transcribe · arquivo, e guarda
sua preferência independentemente da escolha de gravações completas.
Sem Stream, o modelo atual ao parar é usado no reconhecimento; importações
e retranscrições também usam a escolha de gravação completa. O modelo do
Stream fica fixado quando a captura começa, incluindo as duas fontes de Equipe.
Consulte [o catálogo](MODELOS-CLOUD.md) para APIs, permissões e limites.

Transcribe · arquivo usa um bucket S3 existente na mesma região, informado
em Configurações → Conexões → AWS; o app não cria buckets. Transcribe · streaming
usa conexão direta e também aceita áudio já gravado.
Voxtral e Sonic usam Bedrock; os adaptadores Transcribe usam as APIs próprias
desse serviço, com o mesmo perfil AWS selecionado.

O agendamento do app continua reconhecendo pequenos trechos fechados.
Voxtral publica texto de ConverseStream; Transcribe aplica revisões parciais
e guarda somente segmentos finais; Sonic usa somente USER/TEXT/FINAL,
descartando respostas, traduções e áudio gerado pelo assistente.
A latência inclui formar o trecho, a rede e a resposta da AWS; Sonic exige
cadência de áudio em tempo real. Não há promessa de palavras instantâneas.
Gravação e reconhecimento possuem cancelamentos independentes.

## Uso e limites dos motores

O ícone de microfone abre o menu com Microfone, Sistema e Equipe.
Os selects ficam somente nesse menu, que mostra os dispositivos pertinentes
à fonte, com entrada/saída persistidas
por ID estável. O menu alinha sua borda direita ao botão, ajusta-se aos limites
da janela e usa o espaço acima quando não cabe abaixo.
Nomes longos não alargam a janela e erros de salvamento
restauram a escolha anterior. O botão Stream vale para a próxima gravação.
No Stream, cada parte fecha em até quatro segundos; uma screenshot também
fecha a parte na posição capturada. Para preservar palavras cortadas, cada trecho seguinte
inclui até dois segundos anteriores de áudio. A linha do tempo mantém o início
lógico e remove correspondências claras de pelo menos três palavras distintas
na sobreposição, inclusive quando a resposta anterior chega depois. Repetições
ambíguas são preservadas, e a remoção nunca atravessa fontes físicas.
A fila usa duas chamadas AWS simultâneas por padrão, ajustáveis de uma a
quatro, e uma no Whisper, preservando o WAV completo mesmo quando o motor demora.
A linha do tempo offline de Equipe e de gravações com screenshots
usa partes de até **60 segundos no Voxtral** e até **120 nos demais motores**,
respeitando configurações menores. Gravações comuns sem screenshots e
importações mantêm a duração configurada. As duas fontes de Equipe continuam
separadas; o progresso é ponderado pela duração.
O editor mantém o texto anterior durante a espera
e recebe o resultado completo ao concluir, incluindo os links de screenshots
entre as falas. Importações também acrescentam texto, sem ativar Stream.

O gravador mostra a contagem de trechos concluídos e o rodapé
inclui os em andamento, com erro ou para conferir. A porcentagem continua
ponderada por duração, incluindo progresso das chamadas ativas.
Screenshots não contam como partes e faixas com erro não somam sucesso.
Retomar mantém as partes aceitas contadas e a indicação de novas tentativas.
No Stream, esses detalhes aparecem ao concluir os trechos restantes depois
de parar. Consulte [a interface do gravador](GRAVADOR-INTERFACE.md).

Equipe com uma faixa por fonte mostra **Fontes: N de M concluídas**.
Uma gravação de cerca de seis segundos sem Stream nem cortes tem uma faixa
completa do microfone e outra do sistema; **Fontes: 1 de 2 concluídas** indica
que uma delas terminou. Havendo várias faixas, o gravador usa **Trechos** e o
rodapé detalha **Microfone x/y · Sistema x/y**. Fonte única sempre usa Trechos.
O Stream mantém até quatro segundos novos e dois de contexto.

O gravador mantém microfone e Stream na mesma linha de Novo áudio
e Importar. Stream tem somente ícone/tooltip, com realce quando ligado.
Equipe acrescenta um pequeno + ao ícone do microfone. Rótulos e porcentagens
dos volumes têm colunas de largura igual, alinhando o início e o comprimento
das barras mesmo com números diferentes.
O espaçamento do box é fixo, mantendo Gravar/Pausar dentro do painel ao
alternar entre uma e duas fontes, nos três layouts e tamanhos.
As gravações sem Stream ganham cortes na posição dos screenshots depois de
parar. A preparação desses trechos independe do motor; não usa reconhecimento
parcial nem contexto sobreposto nos cortes. O áudio original fica preservado,
e respostas fora de ordem são montadas na linha do tempo. O motor selecionado
ao parar é usado no reconhecimento offline. Sessões antigas mantêm o formato
anterior dos links quando não possuem marcadores temporais.

Os ganhos ficam no popup de um único botão arredondado:
na linha de atalhos, entre **Microfone** e **Stream**, tanto em Médio como
em Grande. O popup acompanha as fontes fixadas para a captura atual;
as barras continuam alinhadas em Equipe.
Duplo clique na bolinha restaura 100%, também no volume independente de
reprodução. O menu do motor contém AWS Cloud, Whisper local e API;
as barras escolhem o modelo contextual sem duplicar submenus.

No estado corrente, `inline_screenshots` determina a posição dos links e
`transcribe_on_stop` distingue reconhecimento offline de Stream no arquivo
da linha do tempo. Novas gravações usam links inline em ambos os modos;
arquivos antigos preservam sua forma de apresentação.
Uma gravação comum sem Stream adquire linha do tempo somente na primeira
seleção de screenshot. Cancelar sua única seleção restaura o reconhecimento
comum quando não há outros marcadores e não é Equipe.
Os cortes usam índices de amostras, incluindo deslocamentos das duas fontes,
e uma seleção concluída após a transcrição mantém a posição já reservada.

Whisper local desabilita Stream porque o caminho atual de whisper.cpp
produz resultado após a inferência. A preferência Stream fica guardada para
voltar à AWS Cloud. Microfone, Sistema e Equipe continuam gravando localmente;
Equipe reconhece as duas fontes depois de parar.

Trechos PCM mono de 16 kHz já gravados são preparados diretamente em Go,
com o mesmo filtro de voz de 80 Hz a 7 kHz e WebRTC VAD. O caminho limitado
a 15 segundos valida cabeçalho/tamanho antes de alocar e evita iniciar
FFmpeg/FFprobe para cada trecho. Fontes de 48 kHz usam o caminho dos codecs,
com o contexto convertido para amostras de 16 kHz. Áudios importados continuam
usando codecs. A detecção considera somente as amostras novas, para que
uma pausa não transcreva novamente a fala usada como contexto.
Cache e prompt de Stream são separados dos resultados de gravações comuns.
O prompt curto pede somente as palavras faladas no idioma original, sem
tradução ou resposta ao conteúdo do áudio. Não inclui glossário nem instruções
extensas, que favoreceram continuações inventadas nos testes.
No pedido de Stream, o áudio precede a instrução curta, que trata comandos
ditados como fala. O prompt comum de arquivos importados permanece separado.
Respostas excessivas recebem uma retentativa limitada e são rejeitadas se
continuarem inadequadas. Limite de saída permite uma tentativa com orçamento
maior, até 8192 tokens, por Converse completo. Uma resposta cortada não vira
parte concluída no cache.
Comentários de incerteza ficam no estado de revisão, fora do texto copiado.
Esses controles não garantem a exatidão de uma transcrição curta.

A fala contínua do microfone usa espaços entre partes; links e mudanças de
fonte separam os blocos. A posição temporal é interna. Edições manuais são
preservadas nas atualizações seguintes, sem duplicar o texto ao parar.
Uma resposta antiga ou cancelada não atualiza outra aba; uma resposta
incompleta remove seu texto provisório e deixa o trecho disponível para
nova tentativa. A rolagem acompanha o texto quando o usuário está no fim.

Equipe mantém as fontes fisicamente separadas. Diarização pode distinguir
pessoas na mesma fonte quando o motor fornece dados confiáveis; IDs permanecem
separados por fonte. Você só é usado com identificação confiável, nunca apenas
porque a fonte é microfone. Sem diarização, usa o rótulo da fonte.
Timestamps internos ordenam segmentos/turnos; sem timestamps, conserva a ordem
dos blocos sem inventar intercalação. Não deduz pessoas do texto.
Nenhuma API Mistral externa nem credencial nova foi adicionada.

WASAPI usa compartilhamento explícito e loopback da saída selecionada.
Silêncio do sistema recebe amostras zeradas para preservar a posição temporal;
pausar descarta amostras nas duas trilhas. Novas gravações ficam
em `gravacoes/AAAA-MM-DD`, pela data local do início, com `.team.json` para
trilhas e `.stream.json` para a linha do tempo. Screenshots usam
`screenshots/AAAA-MM-DD`, pela data do PNG; salvar marcações mantém o arquivo.
Arquivos antigos continuam nos caminhos anteriores, sem migração automática.
Partes fechadas ficam em `streams`; o cache do motor permite retomada.
Uma sessão salva antes da finalização relê o WAV e inclui o trecho final.

## Retomada e retentativas

**Tentar novamente** retoma partes ausentes, com erro ou `needs_review`,
inclusive após reabrir. Na linha do tempo, mantém IDs e textos das partes
aceitas, posições de screenshots e deslocamentos entre fontes.
Na retomada de gravações sem Stream, somente faixas pendentes maiores que o
limite atual são repartidas, sem modificar os WAVs originais. O motor atualmente
selecionado é usado; uma retomada de Stream usa a escolha de Stream,
e uma retranscrição pendente usa a escolha de gravação completa.
Arquivos sem linha do tempo reutilizam o cache compatível com conteúdo
e configurações; mudar o motor pode exigir novo reconhecimento.

**Retranscrever áudio** força reconhecimento
integral com as escolhas atuais, incluindo ambas as trilhas e partes aceitas.
Cada clique cria um cache em `jobs-retranscribe-go-v1/<identificador>`,
preserva o cache anterior e guarda o identificador por aba.
Tentar novamente reutiliza essa tentativa após falha/cancelamento ou reabertura.
O texto anterior permanece durante a espera; falha/cancelamento o preserva.
A atualização mantém edições manuais e os vínculos das screenshots.

A repartição de faixas antigas não é aplicada a arquivos marcados como
Stream, mesmo quando uma retranscrição está pendente. Nesses arquivos,
Tentar novamente conserva os limites das faixas existentes;
Retranscrever áudio cria novas faixas com o limite offline atual.

Erros transitórios do Voxtral recebem até **três tentativas automáticas**,
com prazo novo por chamada e esperas curtas com jitter. O status informa a
retentativa, e o SDK não multiplica as tentativas de Converse/ConverseStream.
Login, permissões, certificado, pedido/modelo/região inválidos e limite
de saída esgotado não entram nesse fluxo de transporte. Correções de saída
truncada, continuação excessiva ou fala curta sem texto são tentativas
semânticas separadas e limitadas; uma resposta incompleta não conclui a parte.
Transcribe, Sonic e Whisper mantêm suas políticas próprias.
Cancelar impede novos envios e interrompe a espera; uma chamada Voxtral já
enviada pode terminar e salvar cache sem atualizar a interface cancelada.
Cancelar Whisper encerra também o processo local.
Retomar enquanto o pedido anterior ainda salva cache mostra a
espera pelo lock, em vez de falhar imediatamente. A espera aceita Cancelar
e prazo do chamador; depois reutiliza as partes concluídas e reenvia somente
as pendentes ou marcadas. O lock anterior permanece sob responsabilidade
daquela tentativa.
Consulte [a política de recuperação](RETENTATIVAS-TRANSCRICAO.md) para prazos,
limites e comportamento de recuperação.

## Reprodução e histórico do áudio

Play/Pausa e Ctrl+Shift+Espaço permitem ouvir, pausar,
retomar e ouvir novamente após o fim, quando a captura terminou e há áudio
carregado. Durante a captura, continuam pausando/retomando o gravador.
Reprodução funciona com importações e Equipe, inclusive enquanto a
transcrição está pendente. **Stop** encerra a reprodução e o próximo
Play começa do início; o alto-falante abre o volume de reprodução de **0 a 200%**.
O ganho atua na cópia privada de reprodução, com limitador suave acima de 100%.
CoreAudio fica em 100% para manter o Painel de sons independente. O ajuste
é salvo ao concluir e restaura o valor anterior se o salvamento falhar.
Os ganhos de captura de
microfone/sistema continuam separados, de 0 a 200%.
Duplo clique na bolinha de qualquer slider de volume restaura 100%.

O contador mostra a posição da reprodução e depois restaura a duração da aba,
sem persistir essa posição temporária. Trocar/fechar a aba, importar outro
áudio, Novo áudio, fechar a janela ou iniciar captura encerram o som;
a nova captura espera a liberação do player. Limpar texto e cancelar
transcrição mantêm a reprodução independente.
WinMM/MCI e a preparação trabalham fora do loop da interface. Equipe mistura
cópias das fontes com seus deslocamentos para ouvir, mantendo os originais
separados para reconhecer; terminar, cancelar ou falhar remove o WAV temporário.

O botão de histórico restaura uma fonte anterior da própria aba,
com sua transcrição acumulada, referência de acréscimo, tentativa e linha
do tempo de Equipe/Stream. Trechos aceitos, pendências e screenshots
acompanham o áudio. O estado atual é preservado antes da troca, os outros
áudios continuam no histórico e resultado/conversa processados permanecem.
Escolher não transcreve automaticamente: Play permite ouvir e Tentar
novamente retoma pendências. A sessão aceita arquivos antigos sem esse campo.
Consulte [reprodução e histórico das capturas](REPRODUCAO-AUDIO.md).

## Implementação e validação

1. Conversa persistida por aba, botão Reprocessar, mensagens Converse e
   validação de limites. Testar contexto exato, edição, erro/cancelamento,
   isolamento entre abas e restauração da sessão.
2. Captura WASAPI compartilhada, loopback e seleção direta no gravador.
   Manter mono PCM s16 e exclusão das pausas. Testar formato, recursos nativos,
   cancelamento, erros e dois clientes simultâneos.
3. Exportação de trechos já gravados, linha do tempo do stream e marcadores
   de screenshots. Testar respostas atrasadas, pausa, seleção tardia,
   descarte, limpeza, troca de aba e ausência de duplicação ao parar.
4. Equipe com trilhas separadas e reconhecimento por fonte física.
   Testar ordem temporal, silêncio em uma fonte, falha de uma captura,
   fim da gravação e compatibilidade do motor.
5. Executar formatação, go vet, suíte, detector de corridas e integração
   de codecs. Validar controles e captura em Windows com dados isolados.
   Preparar pacote e instalação preservando a janela em uso.

## Referências oficiais

- [WASAPI loopback](https://learn.microsoft.com/en-us/windows/win32/coreaudio/loopback-recording):
  captura a mistura reproduzida no dispositivo de saída, em modo compartilhado.
- [Compartilhamento e exclusividade](https://learn.microsoft.com/en-us/windows/win32/coreaudio/exclusive-mode-streams):
  clientes compartilhados podem coexistir; uso exclusivo pode preemptá-los.
- [IAudioClient.Initialize](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nf-audioclient-iaudioclient-initialize):
  modos, formatos, flags e códigos de erro.
- [Dispositivos WinMM e endpoints](https://learn.microsoft.com/en-us/windows/win32/coreaudio/device-roles-for-legacy-windows-multimedia-applications):
  consulta de IDs persistentes via DRV_QUERYFUNCTIONINSTANCEID.
- [Áudio Mistral](https://docs.mistral.ai/capabilities/audio_transcription/):
  Voxtral Realtime e Mini Transcribe V2 possuem APIs próprias. A disponibilidade
  desses recursos na API Mistral não comprova disponibilidade nos modelos
  Voxtral 2507 atualmente configurados no Bedrock.
- [Bedrock ConverseStream](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html):
  eventos de texto, conclusão e uso de tokens, utilizados pelo SDK Go fixado.
- [AudioBlock](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_AudioBlock.html):
  formatos MP3/WAV e bytes usados nas chamadas.
