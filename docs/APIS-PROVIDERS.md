# APIs e perfis

Texto e transcrição de arquivo podem usar OpenRouter, OpenAI ou um serviço
compatível com o protocolo OpenAI. Texto também oferece Azure OpenAI/Foundry
v1; áudio oferece Azure Speech · MAI-Transcribe-2 experimental.
As escolhas de LLM e de áudio são
independentes: usar uma API para texto não altera o motor de transcrição.
Processar e Mesclar compartilham o provider de LLM da aba, com modelos e
raciocínios separados. AWS Cloud e Whisper continuam disponíveis para áudio.

## Configurar

Para texto, abra **Configurações → LLM**, também acessível pelo ícone de
configurações em Processar texto. Escolha AWS Bedrock ou API; configure
separadamente o modelo e o raciocínio de Processar e de Mesclar. O provider
e o endpoint de API são comuns às duas ações daquela aba. Salvar aplica
as opções à aba atual; outras abas conservam suas escolhas.

Para transcrição, escolha **API** em **Configurações → Áudio → Transcrever
com** ou no menu do motor. A conexão, o modelo e o idioma ficam em
**Configurações → Áudio → API**; as barras de qualidade abrem essa seção quando
API é o motor escolhido. O glossário continua na seção Áudio.
**Configurações → Instruções** reúne Teams, Mesclar e os modos personalizados.

Ao configurar uma API:

1. Em **Configurações → Conexões → API**, crie uma conexão, dê um nome e
   escolha OpenRouter, OpenAI, API compatível ou o provider Azure do fluxo.
2. No genérico, informe a URL base completa, incluindo `/v1` se o serviço
   exigir. Nos dois presets, o endereço é fixo.
3. Cole a chave no campo mascarado e clique em **Salvar chave**.
4. Salve a conexão e selecione-a em **LLM** ou **Áudio → API**.
   Use **Consultar modelos / testar conexão** nessa página para preencher
   seu seletor de modelo, ou digite o ID manualmente.
5. Salve as configurações e execute Processar, Mesclar ou a transcrição
   explicitamente.

Consultar modelos faz uma chamada de catálogo com a chave daquele endereço;
não envia texto/áudio nem executa inferência. O catálogo de áudio apresenta
somente modelos cuja capacidade de transcrição é reconhecida pelo contrato:

- OpenAI: IDs das famílias de transcrição conhecidas pelo aplicativo.
- OpenRouter: consulta `/models?output_modalities=transcription`; quando
  existem metadados de saída, eles precisam incluir `transcription`.
- API compatível: metadados `architecture.output_modalities` precisam
  declarar `transcription`.

Um modelo com áudio na entrada e texto na saída de Chat Completions não
comprova suporte ao endpoint dedicado de transcrição. Catálogo indisponível
ou sem capacidade declarada mantém a entrada manual de ID, com aviso.
O acesso a um modelo e suas opções dependem do serviço e da conta.

| Provider | URL base |
| --- | --- |
| OpenRouter | `https://openrouter.ai/api/v1` |
| OpenAI | `https://api.openai.com/v1` |
| API compatível | Endereço informado pelo usuário |
| Azure OpenAI / Foundry | Endpoint HTTPS v1 informado pelo usuário; somente Texto |
| Azure Speech · MAI-Transcribe-2 | Endpoint HTTPS do resource Speech; somente Áudio |

O genérico exige HTTPS. HTTP é aceito somente em `localhost` ou endereço IP
de loopback para um servidor local. A URL não pode conter chave, usuário,
senha, query ou fragmento. Redirecionamentos HTTP não são seguidos.

## Chaves

A chave é uma credencial genérica do **Gerenciador de Credenciais do Windows**,
associada ao usuário atual, à identificação da conexão e ao endpoint completo,
incluindo seu caminho.
Não é salva em `settings.json`, workspace, perfis, rascunhos ou exportações.
Não existe fallback para arquivo de texto. Backups desses JSONs não carregam
a chave para outra máquina.

Texto e áudio reutilizam a chave quando escolhem a mesma conexão do catálogo.
Contas diferentes podem ter a mesma URL base, com IDs e chaves separados.
Trocar de endereço exige salvar a chave correspondente; a chave do endereço
anterior não é enviada. **Salvar chave** substitui a credencial daquele
conexão e endpoint; **Remover chave** a exclui. A interface não preenche a chave antiga
no campo, e as mensagens de erro omitem respostas brutas e headers do serviço.
Uma chave digitada precisa ser salva antes de salvar as opções.
Salvar configurações mantém o formulário aberto enquanto há uma chave
pendente ou uma operação de credenciais em andamento.

O catálogo é compartilhado e admite até dez conexões compatíveis por fluxo,
com seleções ativas independentes. Azure OpenAI/Foundry não é oferecido para
Áudio e Azure Speech não é oferecido para Texto. Conexões anteriores são
migradas automaticamente; somente IDs dessa migração podem recuperar a
credencial antiga vinculada apenas ao endpoint. Criar outra conta na mesma
URL exige salvar sua própria chave.

## Texto, busca e perfis

Texto usa `POST /chat/completions`. Livre envia o texto inicial exato;
Teams e modos personalizados enviam a instrução escolhida. Reprocessar
usa somente a conversa atual da aba, respeitando sua janela de contexto.
Raciocínio é uma opção do modelo; um endpoint compatível pode recusar
parâmetros que não implementa.

No **Azure OpenAI/Foundry**, informe o endpoint v1 e o nome do deployment no
campo do modelo. Esse nome é livre e não identifica a família implantada.
Raciocínio explícito e o parâmetro do limite de saída são configurados
separadamente para Processar e Mesclar. **Automático** usa
`max_completion_tokens` em Azure/OpenAI e `max_tokens` nos demais providers;
os dois parâmetros também podem ser escolhidos explicitamente.

A busca web usa ferramentas cliente e o orçamento geral do app, com globo
independente por aba. O modelo precisa aceitar function calling; a consulta
vai ao buscador público e não depende de credenciais AWS. Ao esgotar o
orçamento, a conclusão recebe as referências encontradas sem oferecer novas
ferramentas. Sem fontes válidas, o app não apresenta uma resposta pesquisada.
Mesclar usa o provider e o endpoint de LLM da aba de origem, com seu próprio
modelo, raciocínio e instrução, sem busca web. Após sucesso, devolve o texto
à Transcrição da primeira aba do grupo; falhas preservam as origens.

Um **perfil nomeado** guarda provider, endpoint, os modelos e raciocínios
de Processar/Mesclar, modo, instruções e busca, sem a chave. Editar um perfil
não troca silenciosamente as opções das abas: selecione/reaplique o perfil
para usar a versão atual.
Uma aba nova herda as opções de texto da última aba do mesmo grupo, com
editores e conversa vazios. O glossário de transcrição permanece compartilhado.

## Instruções e migração

Em **Configurações → Instruções**, Teams e Mesclar têm seus próprios textos
e botões para restaurar a instrução padrão. É possível criar, renomear,
editar e excluir até dez instruções personalizadas, com nomes únicos de
até 80 caracteres e instruções de até 12.000 caracteres. **Guardar
instrução** prepara a alteração; **Salvar configurações** a aplica.

Escolher Livre, Teams, Mesclar ou um modo personalizado não troca provider,
modelo nem raciocínio. Excluir uma instrução em uso exige escolher o modo
substituto para os próximos pedidos. Pedidos enviados guardam uma cópia
das opções e da instrução; renomear, editar ou excluir não altera o
histórico. Um novo envio ou reprocessamento usa as escolhas atuais da aba.

Preferências, perfis e abas anteriores sem opções próprias de Mesclar
recebem o modelo, o raciocínio e a instrução da antiga configuração global
Bedrock, preservando as escolhas de Processar. Se a aba usa API, Mesclar
fica pendente de revisão: escolha um modelo daquele provider em
**Configurações → LLM** antes de executar. A troca de Bedrock para API
também exige revisar o modelo de Mesclar quando ele permanece com o ID
anterior.

## Transcrição de arquivo

Áudio usa `POST /audio/transcriptions`, multipart com arquivo, modelo,
idioma opcional e, quando aplicável, glossário como `prompt`. O app divide
arquivos longos e limita cada upload a 25 MiB de áudio. O serviço pode ter
um limite menor. A resposta precisa conter `text` em JSON.

**Automático** ou idioma vazio omite os campos de idioma e deixa o serviço
detectá-lo. As sugestões oferecem português (`pt`), inglês (`en`), espanhol
(`es`), francês (`fr`) e alemão (`de`); o campo também aceita outros códigos
compatíveis com o modelo.

No preset OpenAI, **`gpt-transcribe`** aceita uma lista separada por vírgulas,
como `pt,en`. Cada código vira um campo multipart `languages[]` separado;
o app não envia `language` junto. O guia desse modelo documenta ISO 639-1,
alguns códigos ISO 639-3 e as variantes chinesas `zh-cn`, `zh-tw` e `zh-hk`.
Isso não estabelece suporte universal a códigos de três letras ou locales.

Outros modelos OpenAI e o OpenRouter recebem um único código ISO 639-1
em `language`; listas são rejeitadas antes do envio. Para áudio misturado
nesses contratos, escolha Automático. A API compatível também usa um
único `language`; confirme os códigos aceitos no serviço, sem presumir
suporte a `languages[]`.

Indicações curtas de idioma aceitas anteriormente, como `pt,en`, `pt-BR` ou
`eng`, são preservadas ao carregar as preferências, sem reescrever o JSON
ou alterar as demais opções. Se o contrato atual do provider/modelo rejeita
o valor, um novo salvamento ou a transcrição exige correção explícita:
escolha um código compatível ou **Automático** antes de continuar.

No OpenAI, **`gpt-4o-transcribe-diarize`** não recebe `prompt` e usa
`chunking_strategy=auto`; o glossário não é enviado para esse modelo.
O contrato OpenRouter aceita `prompt` multipart, mas documenta que o campo
é ignorado, portanto não garante aplicar o glossário.

O fluxo é depois de parar/importar; **API não oferece Stream**.
Idioma, modelo e endpoint de áudio são independentes dos de texto e participam
da identidade do cache. Falhas preservam áudio/texto e permitem tentar novamente.

### Azure Speech experimental

O adaptador envia o arquivo e a definição de **MAI-Transcribe-2** ao recurso
Speech configurado. Solicita diarização e timestamps por palavra, preservando
o texto completo quando os segmentos retornados não cobrem toda a resposta.
Os IDs de pessoas são locais à fonte e à chamada; o aplicativo não deduz
identidades pelo conteúdo das falas. Modelo fixo, endpoint, idioma e chave
precisam ser compatíveis com o recurso provisionado.

## Limites de serviços externos

Catálogo, disponibilidade, custos e precisão dependem do provider e da conta.
Servidores simulados não comprovam inferência real. Azure Speech e
OpenAI/Foundry exigem recursos, deployments, permissões e credenciais
compatíveis; o adaptador não provisiona esses recursos.

## Referências primárias

- OpenAI, Chat Completions:
  `https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create`
- OpenAI, speech to text:
  `https://developers.openai.com/api/docs/guides/speech-to-text`
- OpenRouter, API:
  `https://openrouter.ai/docs/api-reference/overview`
- OpenRouter, contrato STT e modalidades de modelos, na revisão consultada:
  `https://github.com/OpenRouterTeam/docs/blob/e271ac6fe9c5bf423c8b150ac2d44e466d6ad5b7/guides/overview/multimodal/stt.mdx`
  e
  `https://github.com/OpenRouterTeam/docs/blob/e271ac6fe9c5bf423c8b150ac2d44e466d6ad5b7/guides/overview/models.mdx`
- OpenRouter, OpenAPI na mesma revisão:
  `https://github.com/OpenRouterTeam/docs/blob/e271ac6fe9c5bf423c8b150ac2d44e466d6ad5b7/openapi/openapi.yaml`
- Microsoft, CredWriteW:
  `https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew`
- Microsoft, CredReadW:
  `https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw`
