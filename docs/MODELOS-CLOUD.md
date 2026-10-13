# Modelos Cloud

O bucket fica em **Configurações → Conexões → AWS**, junto de perfil/região,
com **Usar bucket S3**, nome e **Testar bucket**. Desmarcar conserva o nome
e impede seu uso; um bucket de versões anteriores continua habilitado.
Áudio → AWS Cloud oferece um link, sem duplicar campos editáveis.

Transcribe por arquivo exige S3 habilitado e configurado. Caso contrário,
o aplicativo preserva áudio/aba e oferece Configurar S3 ou escolher
explicitamente a conexão direta. Não habilita S3 nem troca o motor sozinho.
Salvar essa preferência incompleta é permitido; enviar exige resolver o
pré-requisito. No CLI, `--transcribe-bucket` é uma escolha explícita de uso.

**Testar bucket** usa o perfil/região em edição, identifica a conta autenticada
e a região, envia um pequeno objeto sintético com chave isolada, lê seus bytes
e remove o objeto (e sua versão, quando informada). Cancelar também tenta a
limpeza com prazo próprio; falhas mostram o motivo e eventual limpeza pendente.
O teste preserva a criptografia do bucket e não inicia um job Transcribe:
seu resultado confirma o acesso S3 do aplicativo, sem afirmar permissões
do serviço Transcribe na chave KMS.

Nova 2 e Nova 2.5 têm nomes e IDs separados, com preferências independentes.

Em **Configurações → Áudio → AWS Cloud**, escolha um motor para gravações completas
e outro para **Stream**. Importar, transcrever novamente e reconhecer depois
de parar usam a escolha de gravação completa. O Stream fixa sua escolha
quando a gravação começa. O ícone do motor oferece somente
**AWS Cloud**, **Whisper local** e **API**. Clicar nas barras escolhe o
modelo do modo indicado no tooltip: gravação completa ou Stream na AWS,
catálogo Whisper, configuração de áudio na API.
Whisper local e API ficam sem Stream. Áudio → AWS Cloud mantém as duas
listas completas; escolher um modelo preserva a preferência do outro modo.
Consulte [APIs e perfis](APIS-PROVIDERS.md) para os providers com chave própria.

Preferências anteriores permanecem válidas: qualidade usa Voxtral Small 24B
e rápido usa Mini 3B, até salvar uma escolha explícita. As preferências de
Stream, volume e bucket não alteram o cache do reconhecimento comum.
Modelo e idioma do Transcribe separam seus resultados no cache.

## Catálogo

| Opção | Gravação completa | Stream | API e preparação |
| --- | --- | --- | --- |
| Voxtral Small 24B | Sim | Sim | Bedrock Converse/ConverseStream; MP3 ou WAV |
| Voxtral Mini 3B | Sim | Sim | Bedrock Converse/ConverseStream; MP3 ou WAV |
| Amazon Transcribe · arquivo | Sim | Não | StartTranscriptionJob; WAV temporário no S3 |
| Amazon Transcribe · streaming | Sim | Sim | StartStreamTranscription; PCM mono 16 kHz por HTTP/2 |
| Nova 2 Sonic · ASR experimental | Sim | Sim | InvokeModelWithBidirectionalStream; PCM mono 16 kHz |
| Nova 2.5 Sonic · ASR experimental | Sim | Sim | ID próprio confirmado; InvokeModelWithBidirectionalStream; PCM mono 16 kHz |

São seis opções para gravações completas e cinco para Stream. As duas opções Transcribe são modos do
serviço Standard, e não dois tamanhos de modelo. O catálogo de geração de texto
é independente. Não inclui Transcribe Medical/Call Analytics nem modelos
exclusivos de texto.

IDs utilizados:

```text
mistral.voxtral-small-24b-2507
mistral.voxtral-mini-3b-2507
aws.transcribe.batch
aws.transcribe.streaming
amazon.nova-2-sonic-v1:0
amazon.nova-2-5-sonic
```

Os IDs `aws.transcribe.*` identificam os adaptadores do app; não são IDs
de modelos Bedrock. A escolha roteia para o SDK oficial correspondente,
preservando perfil, certificados da rede e cancelamento.

## Requisitos e limites

Voxtral usa as permissões Bedrock existentes. Nova Sonic é um modelo
speech-to-speech com transcrição. As opções Sonic ficam explicitamente
**experimentais**. Nova 2 conserva `amazon.nova-2-sonic-v1:0`; Nova 2.5 usa
`amazon.nova-2-5-sonic`. As regiões permitidas são `us-east-1`, `us-west-2`,
`eu-north-1` e `ap-northeast-1`; outras regiões são recusadas antes de ler
credenciais. Preferências anteriores de Nova 2 não são transformadas em 2.5.
Somente blocos `USER`, `TEXT`, `generationStage=FINAL` entram no editor.
As respostas `ASSISTANT` e o áudio gerado são descartados pelo app; o serviço
pode cobrar geração mesmo quando o app descarta essa saída.
Nova Sonic 1 está aposentado; modelos sem ID confirmado não são oferecidos.

Transcribe Streaming usa conexão direta; o app envia blocos uniformes de
100 ms sem cabeçalho WAV. O idioma padrão é **pt-BR**.
Também há pt-PT, en-US, es-US, fr-FR, de-DE, it-IT, ja-JP e zh-CN.
O automático identifica entre português brasileiro, inglês, espanhol,
francês e alemão. Uma revisão parcial substitui a anterior pelo ID do segmento;
somente os resultados finais podem concluir a parte e entrar no cache.

Transcribe para arquivos precisa de um **bucket S3 existente na mesma região**.
Informe somente o nome em Conexões → AWS; o app não cria buckets
nem modifica permissões. O perfil precisa iniciar/consultar/remover jobs
Transcribe e enviar/ler/remover os objetos temporários daquele prefixo.
Cada pedido usa `ai-talker/transcribe/aitalker-<ID aleatório>/`.
Após o job terminar, o app lê o resultado e remove somente seus dois objetos
e o job. A AWS não permite remover um job em andamento: cancelar encerra a
espera do usuário, e a limpeza aguarda brevemente um estado final.
Se o serviço continuar ativo ou a limpeza falhar, o namespace pode permanecer
no bucket; o erro/aviso identifica a pendência.

Nas configurações, os nomes mostram **arquivo (S3)** e **streaming (sem S3)**
para distinguir essa preparação; no menu compacto, aparecem como **S3**
e **direto**. Sem bucket, escolher o modelo de arquivo oferece abrir
Conexões → AWS no campo de S3 ou selecionar explicitamente a conexão direta.
Salvar o modo de arquivo com bucket vazio informa o requisito.
Se houver áudio carregado, **Usar sem S3 e tentar novamente** retoma na aba
de origem; áudio, texto anterior e resultado processado ficam preservados.
A conexão direta também aceita arquivos e gravações completas.
O app mantém a escolha de Stream independente e não troca motores em silêncio.

Os serviços Transcribe cobram por duração; Voxtral e Sonic usam os medidores
de tokens da AWS. Região, repetição de contexto e número de pedidos afetam
a medição. Confira a tabela atual do serviço antes de comparar custo.

O **Stream do app continua usando trechos de até quatro segundos**, com até
dois segundos anteriores para contexto. Screenshots fecham a parte na sua
posição original. Escolher uma API Streaming não transforma esse agendador
em uma sessão contínua de microfone; a latência inclui formar o trecho.
Sonic recebe áudio em cadência real e uma cauda silenciosa de dois segundos
para finalizar a fala; essa cauda não altera o arquivo nem os marcadores.
Gravações longas usam partes limitadas, mantendo cobertura até o fim.
Equipe sem Stream usa partes de até um minuto no Voxtral. Falhas temporárias
desse motor recebem retentativas limitadas e prazos novos; retomar preserva
as faixas concluídas. Consulte [a política de recuperação](RETENTATIVAS-TRANSCRICAO.md).

## CLI e verificação

```powershell
.\bin\stt-widget-cli.exe models
.\bin\stt-widget-cli.exe transcribe '.\audio.wav' --model aws.transcribe.streaming --transcribe-language pt-BR --stdout
.\bin\stt-widget-cli.exe transcribe '.\audio.wav' --model aws.transcribe.batch --transcribe-bucket meu-bucket --stdout
.\bin\stt-widget-cli.exe gui --model mistral.voxtral-small-24b-2507 --stream-model amazon.nova-2-sonic-v1:0
```

`--quality quality|fast` continua substituindo a escolha de gravação completa
por Small/Mini. `--model` explícito tem precedência; `--provider whisper`
mantém o reconhecimento local. `doctor` identifica a API do motor escolhido.

Os testes rotineiros usam serviços simulados, PCM sintético e dados isolados:
roteamento, formato, revisão/finalização, tradução/resposta descartadas,
cancelamento e limpeza limitada ao namespace próprio. Inferência real,
disponibilidade, custo e precisão precisam de conferência separada na
conta/região escolhidas.

As métricas de cada parte incluem preparação do cliente, tempo total,
primeiro texto e sua fase (`partial` ou `final`). Conforme a API, também
registram resolução/espera de credenciais, abertura, envio, finalização,
upload S3, consultas do job, download e limpeza. Nenhuma credencial é
incluída nas métricas. O cliente Stream reutilizado conserva o cache e a
renovação de credenciais do SDK; cada trecho continua abrindo sua sessão.
Sem timestamps completos, o texto é conservado na ordem do bloco.
Diarização não presume identidade entre chamadas; casos ambíguos usam o
rótulo da fonte.

## Fontes oficiais

- Bedrock, catálogo: https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards.html
- Voxtral Small: https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-mistral-ai-voxtral-small-24b-2507.html
- Voxtral Mini: https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-mistral-ai-voxtral-mini-3b-2507.html
- Nova 2 Sonic / ID Bedrock: https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-amazon-nova-2-sonic.html
- Anúncio Nova 2.5 Sonic e regiões: https://aws.amazon.com/about-aws/whats-new/2026/10/amazon-nova-2.5-Sonic/
- Eventos de entrada Sonic: https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-input-events.html
- Eventos de saída Sonic: https://docs.aws.amazon.com/nova/latest/nova2-userguide/sonic-output-events.html
- Transcribe Streaming: https://docs.aws.amazon.com/transcribe/latest/dg/streaming.html
- Idiomas Transcribe: https://docs.aws.amazon.com/transcribe/latest/dg/supported-languages.html
- Jobs Transcribe: https://docs.aws.amazon.com/transcribe/latest/APIReference/API_StartTranscriptionJob.html
- Limpeza: https://docs.aws.amazon.com/transcribe/latest/APIReference/API_DeleteTranscriptionJob.html
- Preços Transcribe: https://aws.amazon.com/transcribe/pricing/
- Preços Bedrock: https://aws.amazon.com/bedrock/pricing/
