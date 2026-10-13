# Conversa ao vivo

O modo usa **somente microfone**.
A seleção do modo não inicia a sessão nem envia áudio.
O seletor de modo fica no gravador e em Configurações → Áudio.
Para escolher Nova 2 ou Nova 2.5, use
Configurações → Áudio → Conversa ao vivo → Modelo da conversa ao vivo.
A subcategoria aparece na árvore e na busca; trocar o modelo conserva o modo.

## Serviços e limites

| Serviço/modelo | Conversa contínua | Contrato do aplicativo |
| --- | --- | --- |
| Bedrock `amazon.nova-2-5-sonic` | Implementada; preferência inicial | Áudio e texto bidirecionais; vozes `carolina` e `leo`. |
| Bedrock `amazon.nova-2-sonic-v1:0` | Implementada | Áudio e texto bidirecionais; catálogo próprio de vozes. |
| OpenRouter | Não habilitada neste modo | Nenhum protocolo contínuo integrado; áudio por arquivo não comprova esse suporte. |

O aplicativo não simula conversa contínua com transcrição seguida de TTS.
Não habilitar outro provider sem comprovar seu contrato bidirecional.
Os fluxos convencionais de API permanecem independentes.

As regiões permitidas para Nova 2.5 são `us-east-1`, `us-west-2`, `eu-north-1`
e `ap-northeast-1`. Catálogo e inferência precisam de acesso na conta/região
escolhidas; conferir disponibilidade antes de uma entrega.

## Protocolo e vozes

O cliente usa o SDK oficial Go v2, operação
`InvokeModelWithBidirectionalStream`, com áudio e texto nas duas direções.
Uma sessão envia sistema/histórico antes de abrir um único conteúdo AUDIO
USER. O microfone continua enviando PCM, inclusive silêncio; a API decide as
pausas e responde sem um envio manual por fala. O transporte recebe blocos
de 20 ms na captura; PCM mono de 16 bits em 16 kHz na entrada e 24 kHz na
saída são os padrões do aplicativo. O protocolo também permite 8/16/24 kHz.
`maxTokens` é explícito; endpointing usa `MEDIUM`.
A pausa manual do microfone continua
enviando silêncio de transporte, sem acrescentá-lo ao WAV gravado. A captura
de compatibilidade em 48 kHz é reduzida para 16 kHz, incluindo a sobra final.

A configuração de voz pertence a `promptStart`, portanto trocar voz exige
encerrar e abrir uma sessão nova; áudio já recebido conserva sua voz original.
A tabela oficial de Nova 2 Sonic fornece estes IDs:

| Idioma/localidade | Vozes |
| --- | --- |
| Português brasileiro | `carolina`, `leo` |
| Inglês americano | `tiffany`, `matthew` |
| Inglês britânico/australiano | `amy`, `olivia` |
| Inglês indiano/Hindi | `kiara`, `arjun` |
| Francês | `ambre`, `florian` |
| Italiano | `beatrice`, `lorenzo` |
| Alemão | `tina`, `lennart` |
| Espanhol americano | `lupe`, `carlos` |

Tiffany e Matthew são descritas como vozes poliglotas. Essa tabela documenta
Nova 2, e o app oferece as 16 vozes nesse modelo. O catálogo de **Nova 2.5
oferece somente Carolina e Leo**. O catálogo de Nova 2 não comprova
automaticamente suporte a outras vozes em Nova 2.5.
Ao trocar de modelo, uma voz que não pertença ao
catálogo comprovado é substituída por Carolina para a próxima sessão.
Idioma da interface não determina o idioma da fala nem traduz seu conteúdo.

## Contrato do backend e interrupção

`internal/realtime` fornece sessão com `SendAudio`, `Close`, `Cancel`,
`Interrupt`, `Done`, `Err` e `Snapshot`.
`cloud.NewConversation` reutiliza perfil AWS, certificados e processos de
credenciais ocultos do app. A conexão contínua não herda o timeout total HTTP
das transcrições; preparação, credenciais e cabeçalhos continuam limitados.

Os eventos incluem transcrição USER FINAL, texto ASSISTANT, blocos PCM e
término/interrupção. Texto preliminar ASSISTANT é substituído pelo texto FINAL
realmente falado. Cada callback recebe PCM próprio e texto completo atual,
permitindo persistência e reprodução independentes.

**Nova 2 e 2.5 podem reutilizar o `completionId` do serviço entre falas**,
e `completionEnd` chega ao encerrar o prompt. O app atribui
identificadores lógicos por turno, comuns à transcrição e à resposta, usando
os limites AUDIO `END_TURN`. A segunda fala recebe um ID distinto, mesmo na
mesma conclusão de transporte, inclusive com texto FINAL tardio.

Nova interrompe a geração ao detectar uma nova fala e exige que o cliente
pare a reprodução e esvazie sua fila. `Interrupt` faz essa interrupção local
imediata e suprime novos blocos do turno interrompido, mantendo o microfone
aberto para o barge-in. Não foi inventado um comando wire de cancelamento
por resposta. `Cancel` encerra imediatamente a conexão inteira.
O alvo é fixado no instante de `Interrupt`, para uma transcrição que chega
depois não transferir o cancelamento à próxima resposta. O clique também
impede que PCM já pendente na fila da UI reinicie o autoplay daquele turno.
`Close` fecha AUDIO, prompt e sessão em ordem. Conteúdo parcial é preservado
no snapshot, marcado incompleto, inclusive após erro ou cancelamento.

`EncodeWAV` conserva as amostras e a taxa nativa da resposta; não reutiliza o
cabeçalho de 16 kHz do gravador para uma resposta de 24 kHz.

A documentação da operação Bedrock informa um limite de **oito minutos**
para o stream. Não há
renovação automática: quando o serviço encerra a conexão, o app finaliza a
captura, preserva texto/áudio recebido e permite iniciar outra sessão. A nova
sessão recebe até 38 mensagens recentes de contexto, com falas reconhecidas e
respostas concluídas. Respostas incompletas permanecem no histórico e não são
enviadas como contexto ASSISTANT. O usuário inicia a nova sessão explicitamente.
Erros e respostas incompletas conservam seus arquivos.

O fechamento normal da janela cancela a conexão e conclui captura, callbacks
pendentes e arquivos em segundo plano. Só depois salva o snapshot final e sai.
Não espera o serviço concluir uma resposta nem bloqueia a UI aguardando
WASAPI. A reprodução conserva as amostras originais, permite pausa/seek/volume
independente e, ao receber o fim de áudio, drena a fila nativa antes de liberar
a saída. Stop/interrupção descartam a fila de reprodução e mantêm os WAVs.
A reprodução manual de uma resposta ainda em geração continua recebendo
PCM mesmo com autoplay desligado. O carregamento do prefixo e os callbacks
pendentes usam offsets cumulativos do arquivo, para não repetir nem perder
amostras quando mais áudio chega durante a leitura. O fim de resposta só
fecha esse player depois que o prefixo e os blocos pendentes foram entregues.
Cancelamento durante a conexão inicial também aguarda `Done` antes de fechar
os arquivos; cliques repetidos em Fechar mantêm o mesmo responsável pela saída.

## Fontes primárias

- [API bidirecional Bedrock](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModelWithBidirectionalStream.html).
- [Eventos de entrada Nova Sonic](https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-input-events.html).
- [Eventos de saída Nova Sonic](https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-output-events.html).
- [Barge-in](https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-barge-in.html).
- [Idiomas e IDs de voz](https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-language-support.html).
- [Modelos Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html);
  consultar metadados com `aws bedrock get-foundation-model`.
- [Documentação de áudio OpenRouter](https://openrouter.ai/docs/guides/overview/multimodal/audio).
