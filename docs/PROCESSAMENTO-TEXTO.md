# Processamento de texto, abas e grupos

O segundo editor processa o texto da transcrição somente por ação explícita.
A janela abre por padrão em coluna (gravação → transcrição → processamento),
em `480x900`, mínimo `320x700`. Há rolagem quando a coluna ultrapassa a área
disponível. A opção lado a lado abre em `1280x740`, mínimo `1120x620`;
gravação acima dos editores abre em `1000x740`, mínimo `840x700`.
As abas ficam acima dos dois editores, em uma linha por grupo.
Os temas Claro/Escuro/Verde, layout e tamanho da gravação são persistidos;
mudar a aparência reutiliza os editores e mantém os donos das tarefas.
O botão de tamanho alterna Pequeno/Médio/Grande pela altura na tela principal.
A paleta com bolinha, Fixar, Juntar e Mesclar não exibem texto;
as descrições aparecem em tooltips que preservam o hover e os cliques.
As barras de qualidade mostram o modo escolhido, com quatro preenchidas na
qualidade alta e duas no modo rápido; o tooltip informa o modelo.
Abrir pasta oferece gravações, screenshots e vídeos. Escuro é o tema
padrão. As duas divisórias verticais redistribuem os boxes dentro da coluna,
mantendo rolagem externa e rolagem independente de cada editor.
A barra nativa integra o canvas e conserva seus controles, arraste e bordas
de redimensionamento. No cabeçalho, logo, bolinha AWS e título aparecem uma
única vez. O estado restaurado pertence ao editor, sem anunciar restauração
no gravador.

Em Configurações → Visual, **Inverter transcrição e processamento** troca
a ordem dos dois painéis nos três layouts. A escolha é persistida e mantém
editores, conteúdos, tarefas e estados de recolhimento.

## Modos

O seletor apresenta **Livre**, **Mesclar**, **Teams** e até **dez instruções
personalizadas** com seus próprios nomes. Escolher uma instrução mantém
provider, modelo e raciocínio; essas escolhas continuam independentes.

**Livre** manda somente o conteúdo exato do editor como uma mensagem de
usuário ao clicar em Processar. Não adiciona instrução de sistema,
delimitadores ou histórico nessa chamada inicial.

**Mesclar** combina as transcrições das abas do grupo de origem com uma
instrução própria e devolve o resultado ao painel Transcrição da primeira
aba. O botão Processar executa essa mesclagem quando o modo está selecionado;
o atalho Mesclar da linha de abas continua disponível. O fluxo não oferece
busca web. Juntar, que apenas concatena os textos, permanece uma ação separada.

**Teams** usa uma instrução de sistema editável. Ela explica que a mensagem
do usuário veio de uma transcrição e pode ter erros. Orientações faladas de
destinatário, tom, idioma, extensão e conteúdo fazem parte do pedido de redação.
A resposta deve ser apenas a mensagem pronta para copiar, preservando fatos,
nomes e números. O prompt inicial está em
`internal/processing/processing.go`.

Em **Configurações → Instruções**, as seções Teams e Mesclar permitem editar
e restaurar suas instruções iniciais. Personalizadas permite criar, renomear,
editar e excluir até dez definições: nome único de até 80 caracteres e
instrução de até 12 mil caracteres. **Guardar instrução** prepara a mudança;
**Salvar** nas configurações a aplica. Para excluir uma instrução em uso,
escolha o modo que a substituirá nos próximos pedidos. O histórico enviado
conserva o nome e a instrução utilizados.

**Configurações → LLM** escolhe AWS Bedrock ou API como provider comum a
Processar e Mesclar. Cada operação tem modelo e raciocínio próprios.
As opções aplicam-se à aba atual e à referência para novas abas; abas
anteriores conservam suas escolhas. Uma nova aba do grupo herda suas opções
de processamento da última aba daquele grupo, com textos e conversa vazios.
Trocar instruções não seleciona outro modelo automaticamente.

Os resultados de Processar ficam disponíveis para editar, copiar e salvar.
O globo oferece busca web opcional por aba nos modos que a permitem.

Processar captura o texto e as opções no momento do clique, incluindo a
definição atual da instrução personalizada. O histórico guarda esse conjunto
de provider, modelo, raciocínio, modo, instrução e busca. Alterações posteriores
nas configurações não reescrevem o que foi enviado. Uma edição da origem
ou do resultado cancela a operação pendente e mantém a edição manual.
Trocar de aba mantém o pedido e a resposta
ligados à aba de origem. Alterar modelo/instrução, cancelar, limpar,
usar Novo áudio ou fechar a aba invalida os callbacks antigos.
Gravar novamente mantém os dois editores e acrescenta a nova transcrição.
Concluir o reconhecimento de áudio não inicia Processar/Reprocessar nem cria
um turno processado. Pedidos ou comandos falados continuam sendo conteúdo
a transcrever; o processamento de texto exige uma ação explícita.

## Continuação da conversa

O usuário confirmou **Reprocessar** como continuação da conversa da aba.
Após uma resposta, digite/dite o próximo pedido no box Transcrição e clique
na seta circular ao lado de Processar. O modelo recebe pedidos e respostas
concluídos como mensagens alternadas, seguidos do texto novo exato.
Teams e modos personalizados usam a instrução escolhida; Livre continua sem
instrução adicional.

A última resposta editada pelo usuário substitui aquela resposta no próximo
pedido. Se o resultado visível foi esvaziado, o último turno concluído
continua disponível. Limpar a transcrição preserva a conversa.
**Novo áudio** esvazia os editores para preparar outra gravação, preservando
a conversa ativa e a última resposta editada para sua continuação.
Por exemplo, após P1/R1, Novo áudio e uma gravação P2, Reprocessar envia
P1, R1 e P2; o pedido P3 seguinte inclui também P2/R2. A edição de R1
continua no contexto mesmo quando Novo áudio limpa o resultado visível.
**Processar** começa outra conversa ao enviar, preservando as anteriores
no histórico.
Falha, cancelamento, edição durante a espera e resposta antiga rejeitada
não acrescentam turnos. Abas e grupos continuam isolados.

A conversa é persistida na sessão. Fechar todas limpa também o histórico
da aba que permanece vazia. Não há limite de três versões nem de vinte
respostas no histórico salvo. Para o próximo envio, o app usa uma janela de
até 19 turnos concluídos mais o pedido atual, dentro de 240 mil caracteres.
Se precisar retirar turnos antigos dessa janela, avisa na interface e mantém
todo o histórico para consulta. Conversas anteriores não entram no contexto
de uma conversa nova. Resumo automático permanece adiado.
Busca web não reutiliza tool calls antigos: guarda somente a resposta final
com fontes e executa ferramentas solicitadas no pedido atual.

## Navegação e recolhimento

As setas na transcrição percorrem prompts e respostas da mesma aba, com
indicador de conversa e posição. Uma versão antiga mostra o prompt e sua
resposta correspondente, com ambos os editores bloqueados para consulta
por padrão. O **lápis** habilita a edição explícita do prompt escolhido e
mostra **Reprocessar este turno**; a resposta anterior permanece disponível.
Um prompt ainda sem resposta mostra a última resposta
concluída daquela conversa; voltar mais uma vez pode mudar os dois boxes.

Reprocessar um turno antigo usa as **opções atuais da aba no clique**, sem
restaurar automaticamente o provider, modelo, raciocínio ou instrução usados
na versão anterior. Envia somente os pares concluídos daquela conversa
anteriores ao turno e o prompt modificado, respeitando a janela de contexto.
Ao concluir e salvar, atualiza o prompt e a resposta escolhidos. Os turnos
posteriores mantêm seus textos e não são recalculados.

Com P1/R1, P2/R2 e P3/R3, editar P2 envia P1, R1 e P2 modificado. P3/R3
permanecem; uma continuação P4 usa P1/R1, P2/R2 atualizados, P3/R3 e P4.
O rascunho mais recente e os arquivos de áudio ficam preservados.
Falha, cancelamento, resposta vazia ou erro de salvamento mantém a edição
recuperável e a resposta anterior. A tarefa pertence ao turno escolhido
mesmo durante a navegação, sem substituir outra etapa. Outras conversas e
abas permanecem isoladas.

Ao avançar até o rascunho atual, os editores e Reprocessar voltam ao fluxo
normal. Versões enviadas ficam preservadas mesmo após falha/cancelamento;
editar depois cria/atualiza o rascunho ainda não enviado. Rascunhos não criam
uma versão por tecla. Gravar/importar continuam acrescentando ao texto atual.
Navegar durante uma tarefa não a redireciona; Cancelar permanece disponível.
Histórico de prompts/respostas e histórico de áudios são controles distintos.
O tooltip do indicador informa as opções de envio preservadas; registros
anteriores sem essas opções continuam legíveis.

**Transcrição** e **Processar texto** recolhem independentemente, inclusive
ao mesmo tempo, sem cancelar tarefas nem destruir editores/opções. Em coluna,
cada painel vira faixa horizontal; nos outros dois layouts, faixa vertical.
Expandir recupera o tamanho expandido daquela aba, inclusive com a ordem
invertida.

Os tamanhos da divisão dos editores, da divisão do gravador e da altura total
da coluna pertencem à aba e persistem ao reabrir. Uma nova aba, inclusive a
primeira de outro grupo, copia os tamanhos da aba ativa antes da criação.
Depois, os ajustes são independentes. Sem aba anterior, usa os padrões.
As novas abas começam com os painéis expandidos. Trocar de aba ou desfazer
seu fechamento restaura seus tamanhos, respeitando o espaço disponível.

Os presets e perfis de APIs estão em [APIS-PROVIDERS.md](APIS-PROVIDERS.md).

## Busca web

A chamada usa o AWS SDK for Go v2, `BedrockRuntime.Converse`, com a ferramenta
cliente `web_search(query)`. Somente quando o modelo solicita a ferramenta,
o aplicativo executa a consulta, envia `toolResult` com seu ID e continua a
mesma requisição com o modelo escolhido. Não carrega conversas de outras abas.
Livre conserva sua primeira mensagem exata e não recebe uma instrução de
sistema adicional. Teams mantém a instrução configurada.

A consulta busca o feed público do Bing e devolve resumos e URLs. O aplicativo
não abre páginas nem segue links dos resultados. Os resumos são dados de
referência, não instruções. Para pedidos atuais de USD/BRL, acrescenta os
valores de compra/venda PTAX e a data obtidos pela API pública oficial do
Banco Central. Uma taxa publicada em outro dia conserva essa data e seu tipo;
PTAX não é uma cotação comercial ao vivo. Consultas históricas explícitas
não recebem essa referência atual.

Se o buscador estiver indisponível, há uma alternativa no Nova 2 Lite
(`us.amazon.nova-2-lite-v1:0`) com a ferramenta nativa `nova_grounding`,
em `us-east-1`, usando as mesmas credenciais. Essa alternativa precisa de
acesso ao perfil de inferência e `bedrock:InvokeTool`. A aplicação não altera
permissões IAM. Grounding sem citações é rejeitado; o teste real mostrou que
o Nova pode decidir responder de memória apesar da ferramenta habilitada.

Há **20 buscas por clique** por padrão, ajustáveis de **1 a 100** em
Configurações → Instruções; a escolha geral vale para novos pedidos, inclusive os
por API. Consultas repetidas usam os resultados anteriores, sem consumir
outra busca; ciclos de ferramentas também têm limite.
Ao esgotar o orçamento, o app pede uma conclusão com as referências obtidas
e sem novas ferramentas. Essa finalização não transforma o limite em erro.
Sem fontes válidas, não apresenta uma resposta pesquisada.
No Bedrock, a conclusão recebe somente mensagens de texto e referências,
sem blocos toolUse/toolResult órfãos.

Consultas são limitadas a mil caracteres e
respostas de rede limitadas a 512 KiB. O serviço tem prazo de cinco minutos
com busca e dois sem busca; o desktop acrescenta 30 segundos ao orçamento
total incluindo fila. Cancelar libera a interface imediatamente.
Fontes ficam no resultado persistido e no texto copiado/exportado.
O globo permite desligar busca por aba. Mesclar não oferece ferramentas.
Queries do buscador são enviadas ao Bing; a consulta de PTAX vai ao Banco
Central. O texto completo é processado pelo modelo escolhido, Bedrock ou API.
A alternativa de grounding Nova é exclusiva do fluxo AWS; APIs usam
o buscador público sem credenciais AWS.
Grounding nativo, quando utilizado, também pode gerar cobrança AWS.

Referências primárias:

- Bedrock Converse e ferramentas cliente:
  `https://docs.aws.amazon.com/bedrock/latest/userguide/tool-use-inference-call.html`.
- Nova Web Grounding:
  `https://docs.aws.amazon.com/nova/latest/nova2-userguide/web-grounding.html`.
- Feed consultado: `https://www.bing.com/search?format=rss&q=...`.
- Serviço PTAX: `https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/`.

## Catálogo Bedrock

A lista é explícita em `internal/processing`, sem depender de outro projeto.

| Modelo | ID Bedrock |
| --- | --- |
| Claude Haiku 4.5 (Global) | `global.anthropic.claude-haiku-4-5-20251001-v1:0` |
| Claude Sonnet 5.5 (Global) | `global.anthropic.claude-sonnet-5-5` |
| Kimi K3 (Global) | `global.moonshotai.kimi-k3` |
| GPT-6.1 Sol (Global) | `global.openai.gpt-6.1-sol` |
| GPT-6 Luna (Global) | `global.openai.gpt-6-luna` |
| Grok 4.7 (Global) | `global.xai.grok-4.7` |
| GLM 5 | `zai.glm-5` |
| Nova 2 Lite (Global) | `global.amazon.nova-2-lite-v1:0` |
| Qwen3 Next 80B A3B | `qwen.qwen3-next-80b-a3b` |
| gpt-oss-120b | `openai.gpt-oss-120b-1:0` |

O padrão é GPT-6 Luna. Configurações → LLM aceita um
modelo manual. A permissão para cada modelo continua dependendo
da conta e da região. Converse envia `maxTokens`, sem parâmetros de amostragem
que alguns desses perfis rejeitam.

## Raciocínio e combinação de abas

O raciocínio fica acessível no painel Processar texto por **Auto**, **Baixo**,
**Médio** e **Alto**, além de Configurações → LLM. Auto corresponde a
Automático e não acrescenta parâmetros. No Bedrock, as famílias OpenAI
compatíveis oferecem esses níveis; `gpt-oss` usa
`additionalModelRequestFields.reasoning_effort`; os GPT fechados compatíveis
usam `additionalModelRequestFields.reasoning.effort`.
As outras famílias mantêm Automático, pois possuem esquemas diferentes.
Na API, a compatibilidade depende do modelo escolhido. O aplicativo salva
essa opção por aba, separada da opção de mesclagem; o seletor acompanha
a operação do modo atual.

**Juntar** concatena o conteúdo exato dos editores de transcrição em ordem,
com duas quebras de linha entre textos não vazios. Não chama o modelo.
Só inclui abas do grupo selecionado. O resultado vai para a primeira aba
desse grupo; as demais do grupo são limpas e fechadas.
Os textos dos dois editores são arquivados antes de fechar. O resultado
processado da primeira aba continua independente.

**Mesclar** envia uma lista JSON ordenada de nomes/textos com uma instrução
própria, configurável em Configurações → Instruções → Mesclar.
Modelo e raciocínio ficam em Configurações → LLM e usam o mesmo provider
de Processar. O padrão inicial Bedrock é GPT-6.1 Sol com raciocínio
Automático. Ao migrar uma configuração antiga para API, confirme um modelo
de Mesclar desse provider antes do primeiro envio; o app não usa
silenciosamente o modelo Bedrock anterior.
A instrução orienta preservar fatos e intenções,
retirar repetições, conectar os trechos e respeitar orientações ditadas.
A combinação utiliza a mesma fila de até duas chamadas de texto.

As origens ficam nos editores até a resposta válida. Erro, resposta vazia,
cancelamento, edição, abrir/fechar/renomear abas do grupo ou mudar as opções de
mesclagem impede a substituição por uma resposta antiga. Antes de substituir,
o aplicativo salva a sessão e arquiva os textos originais. Falha ao salvar
mantém os textos e as abas visíveis. A sessão resultante é salva antes
de publicar a combinação na interface.
Selecionar, editar ou criar abas em outro grupo não cancela a mesclagem.
O destino continua sendo a primeira aba do grupo de origem; a seleção
em outro grupo permanece onde o usuário a deixou.

**Desfazer Juntar** ou **Desfazer Mesclar** restaura a operação inteira em
um clique: todas as abas participantes, inclusive o conteúdo anterior da
primeira, nomes, posições, transcrições, resultados, áudio, opções, históricos
e tamanhos. A operação ocupa uma entrada na mesma pilha dos fechamentos.
Antes da restauração, a versão combinada e o trabalho posterior são
preservados em `rascunhos/antes-de-desfazer-*.json`. Os originais e os
rascunhos anteriores continuam preservados. Screenshots excluídas depois
da combinação permanecem excluídas ao desfazer.
Erro ao arquivar ou salvar mantém a versão atual e a entrada disponível
dentro do prazo. A restauração afeta o grupo de origem e não reinicia tarefas
nem aceita respostas das gerações invalidadas.

## Grupos

O menu de **três pontinhos** de cada linha lista suas abas e oferece
**Novo grupo**, **Fechar grupo…**, **Fechar abas deste grupo…** e
**Fechar abas de todos os grupos…**. Clique direito no nome abre o mesmo menu.
Um novo grupo abre uma linha abaixo
das existentes, começando em Aba 1. Cada linha tem seu + e seu menu de
navegação; usar + em uma linha não selecionada cria e seleciona uma aba
daquela linha. Até quatro linhas aparecem sem rolagem do cabeçalho; grupos
adicionais usam rolagem vertical e a linha selecionada é revelada automaticamente.
O desfazer fechamento acompanha a linha selecionada para permanecer acessível.

Aba vazia fecha diretamente. Se qualquer um dos dois editores contiver texto,
uma confirmação avisa que a aba não está vazia e oferece Fechar aba/Cancelar.
Fechar a última aba remove o grupo e seleciona um grupo vizinho.
Desfazer devolve também o grupo, seu nome e sua posição. Quando só há um
grupo, a última aba fechada deixa uma substituta vazia, retirada ao desfazer
apenas se ela não recebeu trabalho novo.
O limite continua sendo 32 abas no total.

Clique com o botão direito no nome do grupo e escolha **Renomear grupo…**.
A opção também fica no menu dos três pontinhos. Renomear aceita de 1 a 40
caracteres em uma linha. O nome permanece ao reabrir,
inclusive depois de Juntar/Mesclar. Nomes curtos usam a largura natural até
o limite ajustável por grupo, de 40 a 320, com padrão 112. Os longos mostram
reticências para manter as ações acessíveis na coluna estreita.

## Estado e preservação

Cada aba tem sua geração de transcrição e de processamento, cancelamento,
editores, erros, áudio e resultado. As rotinas demoradas executam fora do
loop Fyne; a entrega de resultados passa pela fila da interface e confere a
aba e a geração. Até duas chamadas de texto podem acontecer simultaneamente,
e um pedido cancelado enquanto aguarda não inicia uma chamada.

**Desfazer limpeza** guarda um snapshot por aba, com texto exato e referência
do áudio/resultado. Limpezas de um editor já vazio não sobrescrevem esse
snapshot. Uma edição posterior é preservada antes da restauração.
**Desfazer fechamento** acumula uma sequência de fechamentos na barra de abas.
Cada novo fechamento de aba ou grupo dentro do prazo renova **30 segundos**
para as entradas disponíveis. Fechar A, B e C permite restaurar C, depois B
e depois A, com um clique por aba, na posição original e com nome, editores,
áudio, resultados, opções, históricos, tamanhos e desfazer limpeza.
Se o grupo foi removido ao fechar sua última aba, ele também é restaurado
com o nome e a posição originais. A substituta automática da única aba do
aplicativo só é retirada se ainda estiver intocada. Restaurar uma entrada
mantém as outras disponíveis dentro do prazo; Juntar/Mesclar restaura a
operação inteira em sua vez na pilha.
O workspace é salvo antes de publicar a restauração e retirar a entrada.
Uma falha mantém o estado atual e a recuperação disponível.
As gerações já invalidadas impedem que respostas antigas sejam
aceitas mesmo depois de restaurar a aba.
O prazo absoluto e a pilha persistem no workspace; o tempo corre com o app
fechado. Reabrir ou desfazer não renova o prazo.
Consulte [Desfazer persistido](DESFAZER-PERSISTIDO.md).

O fechamento salva `workspace-go-v1.json`, com grupos, abas, seleção, opções,
resultados, históricos, tamanhos, recolhimentos e última limpeza.
Não restaura tarefas em execução. O snapshot
legado `last-transcript.json` continua representando a aba selecionada, e cada
aba também tem um resultado próprio em `abas/<id>/last-transcript.json`.
Arquivos de preferências e transcrições legados continuam legíveis.

Os IDs internos continuam únicos para rejeitar eventos atrasados, mas a
numeração visível segue a ordem das abas abertas dentro de cada grupo. Nomes explícitos são
salvos com `renamed: true` e preservados ao renumerar. Nomes automáticos
antigos como “Aba 99” são corrigidos ao carregar. Botão direito na aba
abre a opção de renomear.
O controle mantém a API de seleção de DocTabs e usa botões públicos do Fyne
nos cabeçalhos, para receber o clique direito no próprio nome da aba.

Fechar abas e substituir respostas preserva textos em `rascunhos/`, inclusive
respostas restauradas que ainda não foram editadas ou exportadas.
Uma sessão inválida não é reescrita ao carregar; antes de salvar a sessão
recuperada, o original recebe uma cópia `workspace-go-v1-invalid-*.json`.

IDs de grupo são estáveis e diferentes dos números visíveis.
Os campos opcionais `groups`, `group_names`, `next_group` e `group` mantêm compatibilidade
com a versão anterior do arquivo; sessões sem grupos migram para Grupo 1.
Grupos duplicados, vazios ou desconhecidos invalidam a sessão inteira,
sem carregamento parcial nem sobrescrita do original.

**Fechar abas deste grupo…** pede confirmação, arquiva os dois editores e
mantém somente a primeira aba vazia naquela linha. O nome do grupo, os outros
grupos, sua seleção e suas tarefas são preservados. A ação usa o grupo do
menu aberto, mesmo quando uma aba de outra linha está selecionada.

**Fechar grupo…** pede confirmação quando alguma aba contém texto; grupos
vazios fecham diretamente. Arquiva os textos e remove a linha com todas as
suas abas. Os outros grupos conservam textos, seleção e tarefas.
Se a seleção estiver no grupo fechado, passa para um grupo vizinho.
Fechar o único grupo cria um novo grupo com uma aba vazia e salva essa
nova sessão. Uma falha de preservação mantém o grupo aberto.
Respostas de tarefas do grupo fechado são rejeitadas, e o desfazer de uma
aba desse grupo é retirado. Gravações e exportações permanecem.

**Fechar abas de todos os grupos…** pede confirmação explícita para todos.
Arquiva os dois editores, salva a sessão vazia antes de alterar a interface
e mantém somente o primeiro grupo com a primeira aba. As tarefas anteriores são canceladas;
gravações e exportações permanecem. Uma falha de preservação mantém as abas.
Durante uma gravação, fechar o grupo de origem e fechar todos ficam indisponíveis;
fechar outro grupo ou suas abas continua permitido.

Cada gravação nova acrescenta ao conteúdo atual da aba de origem, inclusive
edições feitas durante a espera. O snapshot `recording_append` distingue o
texto anterior do trecho reconhecido da gravação. Retomar substitui esse
trecho apenas quando ele não foi editado; não duplica uma resposta parcial.
O botão de retomada também é restaurado após reabrir quando a gravação
terminou com erro ou uma parte ainda precisa de conferência.
Uma gravação silenciosa mantém o texto anterior. Limpar manualmente durante
a captura permite começar outro texto e conserva o desfazer dessa limpeza.
O X ao lado de Pausar pede confirmação para encerrar sem transcrever,
mantendo os editores e restaurando a referência anterior de áudio/resultado.
O arquivo capturado continua na pasta de gravações.

Whisper local segue os mesmos contratos de abas, cache e cancelamento;
consulte [WHISPER-LOCAL.md](WHISPER-LOCAL.md).
