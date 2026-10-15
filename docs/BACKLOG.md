# Backlog — STT-widget

Iniciado em **7 de outubro de 2026**. Revisado em **15 de outubro de 2026**.

## Próxima versão — sugestões registradas em 13 de outubro

O usuário iniciou o backlog da próxima versão, sem definir seu número ou
autorizar implementação, testes, compilação ou release. Os itens abaixo
ficam acumulados para escolha e discussão do próximo lote.

Na conferência prática da 0.12.0, relatou que a **Conversa ao vivo funciona
bem**: detecta pausas, responde com velocidade adequada e oferece uma
experiência próxima à do site usado como referência. Pretende experimentar
em outra máquina; suspeita de latência adicional pela VPN, sem medição
confirmada. Esse retorno positivo sobre o fluxo não constitui aceite de
toda a versão: pediu melhorar o painel Resposta e relatou um bug no player.

| Item | Pedido |
| --- | --- |
| BL-081 | Slider de zoom pela lupa no gerenciador de screenshots. |
| BL-082 | Ferramenta Cortar (crop) no editor de screenshots. |
| BL-083 | Sliders de tamanho do pincel e da borracha. |
| BL-084 | Investigar editor de imagem leve e open source e edição de texto posicionável. |
| BL-085 | Compactar os controles do painel Resposta da Conversa ao vivo. |
| BL-086 | Respeitar a posição escolhida no áudio antes de apertar Play. |
| BL-087 | Fallback de transcrição personalizável em Configurações → Áudio. |
| BL-088 | Azure como provider: LLM no Foundry, transcrição por modelos/Speech e autenticação. |
| BL-089 | TTS do resultado de Processar texto, com escolha de motor e player compacto. |
| BL-090 | Investigar seleção de modelos no Amazon Transcribe. |
| BL-091 | Exibir “Pronto para outro áudio” em verde. |
| BL-092 | Reabrir Configurações na última página/subcategoria visitada. |
| BL-093 | Reduzir à metade a largura mínima do layout com gravação acima dos editores. |
| BL-094 | Investigar bloqueio de edição e retomada da transcrição com screenshot. |
| BL-095 | Adicionar Maximizar/Restaurar à barra da janela principal. |
| BL-096 | Disposição vertical do gravador no layout lado a lado. |
| BL-097 | Pesquisar wake word para o modo Conversa ao vivo. |
| BL-098 | **Abandonado pelo usuário** — roteamento dos efeitos para outros aplicativos. |
| BL-099 | Bypass do pré-processamento de transcrição em Configurações → Áudio. |
| BL-100 | Pesquisar execução da suíte de testes na cloud para reduzir espera local. |

### BL-081 — Slider de zoom pela lupa

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

No gerenciador de screenshots, manter o select de zoom com valores
discretos. Clicar na lupa deve abrir um **slider de zoom**, em vez de abrir
esse select. O slider pode aparecer logo abaixo do próprio controle,
aproveitando o espaço disponível. Faixa e passos ficam para discussão.

### BL-082 — Ferramenta Cortar

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

Adicionar um botão **Cortar (crop)** no editor de screenshots. O pedido
define a ferramenta; seleção da área, confirmação, salvamento e interação
com Desfazer/Refazer precisam ser detalhados no planejamento do lote.

### BL-083 — Sliders de tamanho do pincel e da borracha

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

Oferecer slider para ajustar o tamanho do traço quando estiver em modo
**pincel** ou **borracha**, encaixado no espaço do editor. Atualmente o
usuário encontra valores discretos. Ainda não definiu se esses controles
discretos permanecem nem se cada ferramenta guarda seu tamanho separadamente.

### BL-084 — Investigar editor leve e texto posicionável

Registro: **13 de outubro de 2026**. Estado: **investigação futura registrada**.

Investigar a possibilidade de integrar um editor de imagem **leve e open
source**, evitando reconstruir individualmente todas as ferramentas.
GIMP foi citado como referência de recursos, mas considerado pesado pelo
usuário; procurar uma alternativa menor, compatível com o aplicativo.
Comparar integração, peso da distribuição, licença, manutenção e recursos
com continuar evoluindo o editor atual. Nenhum candidato foi escolhido.
Se a integração não compensar, continuar com a implementação própria.

Como exemplo de melhoria desejada, ao adicionar texto permitir **selecionar
e arrastar o texto na imagem**, em vez de depender somente do modal atual
para escrever. Detalhar posicionamento e edição posterior. Relacionar a
investigação com BL-081 a BL-083; a escolha de um editor externo ainda não
está decidida e a pesquisa não foi iniciada neste registro.

### BL-085 — Controles compactos no painel Resposta

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

O fluxo speech to speech recebeu retorno positivo, mas os selects largos,
texto pequeno e controles distribuídos em muitas linhas comprimem a área
da resposta. Compactar os controles para devolver espaço ao conteúdo.

A captura `Screenshot_2026-10-13_06-54-42.870163500.png` foi conferida durante
o registro, mas removida na desinstalação. As marcações roxas indicavam o
agrupamento e posicionamento descritos abaixo; a imagem não está disponível.

- Reunir **seletor de voz, slider de volume e checkbox de reprodução
  automática** em uma linha, com largura adequada à voz.
- Usar outra linha compacta para **Play, progresso/seek e escolha da saída**;
  o usuário sugeriu um ícone para selecionar a saída e slider de progresso
  menor, conforme a referência.
- Manter a ação de **atualizar saídas** junto da seleção da saída, na mesma
  linha. Preservar acesso aos demais controles de reprodução/interrupção.

Durante a descrição o usuário considerou voz e saída na mesma linha e
depois refinou para voz/volume/checkbox juntos e saída junto do player.
Essa última organização é a direção registrada; distribuição exata e
comportamento em janela estreita ficam para discussão do lote.

### BL-086 — Play ignora o seek escolhido antes de tocar

Registro: **13 de outubro de 2026**. Estado: **bug relatado pelo usuário**.

No áudio do painel Resposta, clicar na metade do progresso **antes de
apertar Play** faz a reprodução voltar ao início. Clicar na metade
**durante a reprodução** avança corretamente, segundo o relato.

Investigar e corrigir para que Play comece na posição escolhida previamente.
Na implementação futura, conferir seleção antes de tocar, seek durante
reprodução e interação com pausa/Stop, sem confundir a seleção explícita
de uma posição com o reinício esperado após Stop. Ainda não reproduzido
tecnicamente nesta sessão.

### BL-087 — Fallback de transcrição personalizável

Registro: **13 de outubro de 2026**. Estado: **sugestão e pesquisa futura registradas**.

Trazer para **Configurações → Áudio** a configuração personalizável do
fallback de transcrição, junto das escolhas do motor de reconhecimento.
O usuário descreveu o fluxo atual como falha na AWS seguida de recuperação
pelo Whisper com o modelo local selecionado. A documentação histórica de
BL-015 limita esse fallback ao Voxtral 24B, fora de Stream e com Whisper
já configurado; conferir o código para mapear o comportamento real antes
de propor a ampliação, sem presumir cobertura de todos os motores AWS.

Como possibilidades, o usuário citou **fallback primário, secundário e
terciário**, **número de retentativas** e **tempos/prazos**. São exemplos
para pesquisa, não uma cadeia ou conjunto de campos já decidido.

No planejamento do lote, o implementador deverá:

- Mapear opções existentes, valores fixados no código, gatilhos de falha,
  retentativas, esperas e limites de tempo atuais.
- Avaliar quais opções fazem sentido expor e quais exigem estender o
  código, incluindo motores/modelos elegíveis e alcance por fluxo.
- Propor a interface, os padrões e limites, distinguindo prazo por
  tentativa, espera entre retentativas e orçamento total quando aplicável.
- Apresentar a recomendação e **alinhar as decisões com o usuário** antes
  da implementação; quantidade de níveis e parâmetros continuam abertos.

Relacionar com BL-015 e `docs/RETENTATIVAS-TRANSCRICAO.md`. A proposta deve
considerar cancelamento, preservação de áudio/texto parcial e disponibilidade
dos motores configurados. Este registro não inicia a pesquisa ou altera o
fallback atual.

### BL-088 — Azure como provider de LLM e transcrição

Registro: **13 de outubro de 2026**. Estado: **sugestão e pesquisa futura registradas**.

**Esclarecimento do usuário em 13/10:** Azure deve ser um **provider do
aplicativo**, abrangendo **LLM pelo Microsoft Foundry** e **transcrição**,
além de eventual TTS. A interpretação anterior que limitava este item a
Azure TTS foi corrigida. TTS é um recurso independente, tratado em BL-089.

No planejamento do lote:

- Mapear o suporte existente de BL-049 (Azure Speech para transcrição) e
  BL-050 (Azure OpenAI/Foundry para texto), distinguindo adaptadores atuais
  da experiência desejada de Azure como provider.
- Investigar catálogo e seleção de modelos/deployments de LLM no Foundry.
- Verificar se modelos disponíveis aceitam áudio para transcrição, em um
  fluxo comparável ao uso atual de modelos no Bedrock, sem presumir que
  qualquer LLM aceite áudio.
- Avaliar Azure Speech como motor de reconhecimento dedicado, comparável
  ao papel do Amazon Transcribe; pesquisar se há escolha de modelo e quais
  opções realmente existem. Relacionar a dúvida sobre Transcribe com BL-090.
- Investigar autenticação externa ao app, com login pela ferramenta oficial
  e seleção de conta/contexto no aplicativo. O usuário deseja uma experiência
  semelhante ao login e perfil AWS. Verificar o mecanismo real do Azure,
  sem assumir equivalência de perfis ou que um nome de perfil baste.
- Propor configuração, capacidades e limites e alinhar as decisões com o
  usuário antes da implementação.

**Limite de validação informado:** nesta máquina o usuário não tem acesso
ao Azure e não consegue criar recursos no console. Poderá testar em outra
máquina e, se necessário, executar verificações essenciais e informar os
resultados. Considerou uma primeira implementação aqui seguida de teste
lá, desenvolvimento da parte Azure na outra máquina ou trabalho híbrido;
a estratégia ainda não foi escolhida.

Planejar validação local e integração real separadamente, registrando o
que depende de recursos/permissões externos. Não apresentar testes locais
como prova de funcionamento real no Azure. Nenhuma pesquisa ou
implementação foi iniciada neste registro.

### BL-089 — Text to speech em Processar texto

Registro: **13 de outubro de 2026**. Estado: **sugestão e pesquisa futura registradas**.

Adicionar **text to speech (TTS)** ao resultado do painel **Processar
texto**, permitindo ouvir o texto processado. Avaliar **Piper local**,
**Azure TTS** (BL-088) e alternativas por **APIs/AWS**, com possibilidade
de escolher o motor/provider. **TTS é independente da inclusão do Azure
como provider**: avaliar síntese nos providers disponíveis (AWS, API/
OpenRouter, Azure) quando houver suporte real, além da alternativa local.
Não presumir que a existência de um provider ou de um LLM garanta TTS.
São candidatos para pesquisa, sem seleção de biblioteca, serviço ou
modelo já fechada.

Manter a interface discreta, pois o usuário prevê uso eventual:

- Concentrar escolha de motor/provider e demais opções em
  **Configurações**, com localização a definir.
- No painel, oferecer **seletor de voz**, **Play/Stop** e uma **barra de
  progresso do áudio**, próximos entre si, em um canto pequeno.
- Evitar acrescentar selects largos ou muitas linhas de controles ao
  painel; a escolha de motor fica nas configurações.

No planejamento, comparar integração, vozes/idiomas, instalação e peso
local, credenciais e custos dos serviços. Propor padrões e esclarecer o
texto a sintetizar, comportamento após edição, geração/reutilização do
áudio e relação com o histórico antes de implementar. O alcance confirmado
é a leitura de Processar texto; não confundir com a Conversa ao vivo
speech to speech. Pesquisa e implementação aguardam a escolha do lote.

### BL-090 — Seleção de modelos no Amazon Transcribe

Registro: **13 de outubro de 2026**. Estado: **dúvida e pesquisa futura registradas**.

O usuário perguntou se Amazon Transcribe oferece seleção de modelo;
não sabe se essa opção existe. Investigar as capacidades reais para os
fluxos usados pelo app, distinguindo escolha de modelo de outras opções
de reconhecimento ou personalização. Se houver uma seleção aplicável,
propor sua exposição nas configurações e alinhar o escopo com o usuário.
Relacionar com a investigação de modelos/opções do Azure Speech em BL-088.
Não foi afirmada a existência dessa capacidade nem iniciada a pesquisa.

### BL-091 — “Pronto para outro áudio” em verde

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

A mensagem **“Pronto para outro áudio”** também deve aparecer em **verde**,
seguindo a indicação de prontidão de “Pronto para começar” (BL-074).
O pedido altera a cor da mensagem, sem definir mudança de texto ou de
comportamento. Implementação aguarda a escolha do próximo lote.

### BL-092 — Lembrar a última página de Configurações

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

Ao reabrir **Configurações**, voltar à mesma página/aba ou subcategoria
em que o usuário estava, tanto após **Salvar** quanto após **fechar**
a janela. Lembrar a localização independentemente de salvar alterações
nos valores; fechar sem salvar não deve impedir lembrar a página.

O pedido trata da seleção na navegação. Posição de rolagem e conservação
da localização após reiniciar o aplicativo não foram especificadas.
Implementação aguarda a escolha do próximo lote.

### BL-093 — Largura mínima do layout com gravação acima

Registro: **13 de outubro de 2026**. Estado: **problema relatado e ajuste solicitado**.

No modo visual com **gravação acima dos editores**, o usuário não consegue
estreitar suficientemente a janela e considera a largura inicial/mínima
excessiva. Pediu que a **largura mínima seja aproximadamente metade da
atual**, tomando a captura como referência. Conferir restrições de tamanho
dos painéis, inclusive com Processar texto recolhido, e adaptar os controles
à largura menor no planejamento do lote.

A captura `Screenshot_2026-10-13_07-35-04.295167000.png` foi conferida no
registro, mas removida na desinstalação. Mostrava gravação acima dos editores, Transcrição
expandida e Processar texto recolhido à direita. A captura isolada não mede
o limite de redimensionamento; o bloqueio é relato do usuário.
Implementação aguarda a escolha do lote.

### BL-094 — Edição e retomada com screenshot na transcrição

Registro: **13 de outubro de 2026**. Estado: **bug relatado; avaliação inicial de leitura**.

O usuário relatou não conseguir editar a última transcrição nem
transcrever novamente/retomar uma tentativa que aparentou falhar.
Depois associou o bloqueio à presença de **screenshot na transcrição**.
Investigar esse cenário, preservando áudio, texto, links, histórico e
edições; a causa técnica ainda não foi confirmada.

A captura `Screenshot_2026-10-13_07-37-59.859985900.png` foi conferida no
registro, mas removida na desinstalação. Mostrava link “Screenshot 1”, navegação “Conversa 1 · 4 de 4”,
ícone de lápis no rodapé e botão “Tentar novamente”. O aviso diz:
“O modelo indicou ausência de fala na primeira tentativa; confira o texto
recuperado.” A gravação também aparece em andamento. Esses estados devem
entrar na reprodução futura, sem inferir apenas pela imagem falha integral
do reconhecimento.

**Avaliação inicial do código, sem execução ou alteração:** em
`internal/desktop/links.go`, a apresentação com links pode esconder o
editor e mostrar uma leitura não editável. O botão definido em
`internal/desktop/documents.go` deve alternar essa leitura para edição e
focar o editor. Inserção de screenshots e algumas atualizações chamam
`updateTranscriptLinks(..., true)`; conferir se essas transições devolvem
indevidamente o usuário à leitura durante uma edição.

Na investigação do lote, distinguir modo de leitura, funcionamento do
lápis, atualizações durante gravação/reconhecimento e eventual bloqueio
de Tentar novamente/Retranscrever. A associação com screenshots é hipótese
do usuário compatível com a troca de apresentação observada no código,
mas não prova ainda a causa de todos os sintomas. Não foram iniciados
implementação, testes ou reprodução nativa.

### BL-095 — Botão Maximizar/Restaurar na janela principal

Registro: **13 de outubro de 2026**. Estado: **ausência relatada e inclusão solicitada**.

O usuário identificou que a barra da janela principal oferece minimizar,
fechar, fixar e mudar tema, mas falta **Maximizar**. Adicionar o botão,
com ação de **Restaurar** quando a janela estiver maximizada.
Este pedido refere-se à janela principal, além dos controles da janela
do Painel de sons já tratados em BL-080. Implementação aguarda o lote.

### BL-096 — Gravador vertical no layout lado a lado

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

No layout com os editores/boxes **lado a lado**, o usuário considera o
gravador excessivamente horizontal (“deitado”). Pediu que sua disposição
tenha o **lado maior na vertical**, usando como referência uma **rotação
de 90 graus no sentido anti-horário** da box de gravação.

O usuário sugeriu investigar se é possível reaproveitar ou transformar o
componente inteiro, sem reescrever tudo. A possibilidade de rotação direta
é hipótese técnica, não solução já verificada. No planejamento, avaliar
reaproveitamento do componente e apresentar a disposição dos controles
para alinhar o resultado visual pretendido. O pedido se aplica ao layout
lado a lado; detalhes de dimensões e orientação de textos/ícones ainda
não foram definidos. Implementação aguarda a escolha do próximo lote.

### BL-097 — Wake word na Conversa ao vivo

Registro: **13 de outubro de 2026**. Estado: **ideia para pesquisa futura**.

Pesquisar **wake word (palavra de ativação)** para o modo **Conversa ao
vivo**. O usuário ainda não definiu palavra, motor ou comportamento da
ativação. O recorte específico deste pedido é a conversa; as demais
direções de comandos de voz e automação continuam sem escopo definido.

No planejamento, avaliar alternativas, idiomas, execução local ou remota,
consumo de recursos, licenças e falsos acionamentos. Propor e alinhar quando
o microfone fica escutando, quando começa o envio ao provider e se a
palavra inicia uma sessão ou habilita falas individuais, além da forma de
encerrar/voltar à espera e da indicação visual do estado.
Esses pontos são questões de pesquisa, não decisões já tomadas.
O registro não inicia pesquisa ou implementação.

### BL-098 — Incluir efeitos do Painel de sons na captura

Registro: **13 de outubro de 2026**. Estado atual: **abandonado pelo usuário em 13/10**.

O usuário retirou este pedido por considerar que o uso de dispositivo
virtual não faz sentido para sua necessidade. **Não pesquisar nem
implementar este item no próximo lote.** A necessidade técnica de
dispositivo virtual não chegou a ser verificada; o cancelamento é uma
decisão de produto.

O objetivo retirado era disponibilizar os efeitos como entrada para outros
aplicativos, não apenas misturá-los na gravação interna. O retorno positivo
sobre o Painel de sons não representa aceite integral do produto.

### BL-099 — Bypass do pré-processamento de transcrição

Registro: **13 de outubro de 2026**. Estado: **sugestão registrada**.

Oferecer em **Configurações → Áudio** uma opção para habilitar/desabilitar
o pré-processamento usado antes da transcrição. Manter **habilitado por
padrão**; ao desabilitar, fazer **bypass** desse tratamento, permitindo
transcrever sem o pré-processamento ou com o tratamento já montado.

O usuário mencionou um filtro de aproximadamente **80 Hz a 7 kHz**, sem
certeza dos valores. Conferir no código a existência, os parâmetros e o
alcance do tratamento atual antes de implementar; a faixa citada não é
especificação confirmada. Mapear os fluxos/motores afetados e distinguir
esse tratamento das conversões de formato necessárias ao reconhecimento.
O pedido não inclui novos filtros ou estudos de normalização.

A preferência deve ser persistida. Propor o alcance e a aplicação da
mudança aos trabalhos novos no planejamento do lote, preservando áudios
originais. Este registro não inicia investigação, implementação ou testes.

### BL-100 — Testes na cloud

Registro: **13 de outubro de 2026**. Estado: **pesquisa de infraestrutura registrada**.

O usuário considera a execução local dos mais de 400 testes excessivamente
lenta e sugeriu **Azure DevOps**, perguntando por equivalente na AWS.
Pesquisar CI remoto para tirar essa carga da máquina de desenvolvimento
e reduzir o tempo de espera, avaliando Azure Pipelines e AWS CodeBuild,
sem escolha de serviço ou autorização de provisionamento/gastos.

Consulta inicial à documentação oficial confirmou serviços de execução
de testes na cloud e opções Windows:
AWS CodeBuild (`https://aws.amazon.com/codebuild/faqs/`) e imagens
(`https://docs.aws.amazon.com/codebuild/latest/userguide/sample-github-action-runners-update-yaml.images.html`);
Azure Pipelines, agentes
(`https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/agents`)
e particionamento de testes
(`https://learn.microsoft.com/en-us/azure/devops/pipelines/test/parallel-testing-any-test-runner`).
Isso confirma candidatos, não compatibilidade já validada com o projeto.

No planejamento, medir os gargalos e comparar custo, disponibilidade
Windows, toolchain Go/CGO, dependências nativas, cache e distribuição dos
lotes entre workers, mantendo a cobertura funcional e com detector de
corridas. Avaliar separadamente testes automatizados, integrações e
conferências nativas que dependam de desktop, áudio ou dispositivos reais.
Execução remota por si só não garante uma suíte mais rápida; propor
estratégia e estimativa com evidências. Não foi criado pipeline ou recurso.

## Preparação do repositório público

| Item | Pedido |
| --- | --- |
| BL-101 | Escolher novo nome e preparar Git/GitHub para publicação pública, protegendo informações sensíveis. |

### BL-101 — Novo nome e Git/GitHub público

Registro: **13 de outubro de 2026**. Estado: **preparação e publicação dos
fontes concluídas; validação funcional pendente**.

Ao retomar o projeto em outra máquina, o usuário pediu primeiro escolher um
novo nome e configurar Git/GitHub, com intenção de manter o repo público e
cuidado explícito com informações sensíveis. O nome escolhido é
**STT-widget**. Foi aplicado à documentação, à identidade visível, às
traduções e aos scripts/launchers. Os próximos executáveis serão
`STT-widget.exe` e `stt-widget-cli.exe`. IDs de dados/credenciais, módulo Go,
entradas em `cmd` e ícones originais foram preservados.

Esta transferência não trouxe `.git`. O usuário escolheu **começar um
histórico novo**. Foi inicializado Git local em `main`, com alias público
e e-mail `noreply` da conta autenticada configurados somente neste repo.
O repositório público é [Apethor/STT-widget](https://github.com/Apethor/STT-widget),
com `origin/main` e primeiro commit de fontes `7bc334c`.
O `.gitignore` foi reforçado para dados locais, backups e chaves privadas,
preservando assets incorporados e fixtures sintéticas. A triagem inicial e
as decisões para publicação estão em [REPOSITORIO-PUBLICO.md](REPOSITORIO-PUBLICO.md).

O usuário escolheu **MIT** para o código original; `LICENSE` foi adicionado
com o alias público como titular. Autorizou substituir os dois sons de Mario
por efeitos livres, inclusive síntese original, sem exclusividade do projeto.
Foram gerados **Impulso** e **Brilho**, dedicados ao domínio público via CC0.
Os áudios Nintendo foram removidos dos fontes antes do primeiro commit.
O catálogo migra para versão 3, preservando IDs, nomes pessoais, volume,
posição e cópias já instaladas. Os testes de migração dos catálogos 1/2
passaram também com detector de corridas na validação de BL-102.
A conferência nativa integral e o aceite funcional continuam para a entrega futura.
A revisão inicial por padrões não é uma auditoria completa;
se o histórico antigo vier a ser importado, precisará de análise própria.
Não há autorização para implementar BL-081 a BL-100, recompilar ou gerar
uma release nesta etapa. BL-098 continua abandonado.

## Retomada do desenvolvimento

| Item | Pedido |
| --- | --- |
| BL-102 | Preparar o ambiente local e validar a base atual antes de escolher funcionalidades. |

### BL-102 — Preparar e validar a base nesta máquina

Registro: **13 de outubro de 2026**. Estado: **ambiente preparado e validação
automatizada concluída; aceite funcional pendente**.

Depois da publicação dos fontes, o usuário autorizou prosseguir com o
desenvolvimento e escolheu preparar/validar a base, incluindo renomeação e
migração dos efeitos. Pediu também remover do README a frase
“Anteriormente chamado AI Talker.”, que foi retirada.

Usar as versões e os hashes fixados em `scripts/toolchain.json`. Ferramentas,
caches de módulos/compilação e intermediários ficam em `artifacts`, ignorada
pelo Git. Dados dos testes permanecem sintéticos e isolados; esta etapa não
inclui chamadas a serviços reais ou alterações na instalação global.
Executar os testes necessários, corrigindo falhas da base que aparecerem.
Não iniciar BL-081 a BL-100 sem o lote escolhido; BL-098 segue abandonado.
A validação automatizada não substitui o aceite funcional do usuário ou a
conferência nativa de uma entrega futura.

**Resultado:** toolchain fixada preparada localmente, hashes conferidos e
caches/intermediários isolados em `artifacts`. O runner foi corrigido para
selecionar um único Go quando o local e o global coexistem. A frase pedida
foi removida do README, e o metadado Fyne foi alinhado à base 0.12.0.

`test-go.ps1 -Integration` passou: formatação, `go vet`, módulos, detector
de corridas, 448 testes desktop em 23 lotes em cada modalidade e integrações
locais. Compilação CLI/GUI local passou; versão e ajuda do CLI passaram.
`smoke-desktop.ps1 -IntegratedFrame -Screenshot` conferiu abertura, título,
ícones, moldura, controles nativos, instância única e encerramento com dados
sintéticos. Sobre foi coberto por teste automatizado; a inspeção visual
dessa página no executável da entrega segue pendente. Não houve release
ou serviços AWS reais. Evidências locais e limites estão no [HANDOFF](../HANDOFF.md).

## Direção web/SaaS

| Item | Pedido |
| --- | --- |
| BL-103 | Organizar o STT Hub em pasta/repositório independentes, preservando a origem e a possibilidade de continuar o desktop. |

### BL-103 — STT Hub: pivot para web/SaaS

Registro: **15 de outubro de 2026**. Estado: **organização inicial concluída;
recorte final, stack e implementação da aplicação pendentes**.

O usuário quer transformar a experiência em um produto web/SaaS, escolhendo
as funcionalidades que permanecem e as que ficam de fora. Confirmou como
público inicial **profissionais individuais, para transcrever e trabalhar
textos**. Autorizou a preparação em pasta paralela, repositório próprio,
primeiros commits, GitHub e continuidade em outra sessão. Na mesma preparação,
definiu explicitamente GitHub **público** e retomada em outra máquina, com
referências ao código de origem por URLs do GitHub, sem arquivos locais.
Quer preservar as decisões e os comportamentos úteis desta base.
Escolheu **STT Hub** como nome do SaaS, sem levar os identificadores legados
do desktop. Esclareceu que o STT-widget pode ser retomado, inclusive em
paralelo; a transição não encerra este projeto.

A referência atual é [Apethor/STT-widget](https://github.com/Apethor/STT-widget),
commit `361788018f841637d09c3931f64e8b2819d16347`.
Há núcleo compartilhado entre CLI e desktop em `internal/transcript`, regras
e prompts em `internal/processing` e documentação funcional em `docs`.
O núcleo ainda usa configurações, armazenamento e recursos locais;
reaproveitá-lo exige avaliar essas dependências para o serviço multiusuário.

Proposta inicial para discussão: gravar pelo microfone ou enviar áudio,
transcrever, revisar, processar por ação explícita, copiar/exportar e
consultar histórico. Captura do sistema, screenshots/vídeo, Painel de sons,
Conversa ao vivo e execução local do Whisper são candidatos a ficar fora
da primeira versão; esses cortes ainda não foram aprovados pelo usuário.

**Resultado da organização:** pasta irmã **STT-Hub**, com Git próprio em
`main` e repositório público [Apethor/stt-hub](https://github.com/Apethor/stt-hub).
A base nova tem README, AGENTS, handoff, escopo, decisões, contratos,
backlog próprio e mapa de origem com links para a revisão de referência.
Uma referência de prompt foi adaptada, com aviso MIT preservado.
Não foram importados fontes Go, ferramentas, assets nem dados de usuário.
Clone anônimo conferiu os 12 arquivos e 28 links internos, com árvore limpa;
a API pública confirmou a visibilidade e a branch padrão `main`.

O novo projeto não depende de um caminho local desta máquina. Go no backend
continua como possibilidade. As decisões de produto/frontend/providers
seguintes estão em **BL-104**; backend, hospedagem, limites de uso, cobrança
e licença do código futuro seguem em aberto.
Ainda não há scaffold da aplicação nem implantação. O backlog desktop não
integra automaticamente o escopo do SaaS; BL-098 permanece abandonado.

## Decisões de produto do STT Hub

| Item | Pedido |
| --- | --- |
| BL-104 | Consolidar o recorte, frontend, direção AWS e investigações do STT Hub no GitHub antes da implementação. |

### BL-104 — Recorte e decisões do STT Hub

Registro: **15 de outubro de 2026**. Estado: **decisões documentadas;
investigações práticas e implementação pendentes**.

O usuário pediu continuar a discussão nesta sessão e registrar no repo
novo somente contexto útil para o produto. Definiu Angular com o padrão
Orquestra, cujas skills/componentes serão obtidos no ambiente de destino.

Manter microfone da própria pessoa pelo navegador, screenshots, processamento,
abas/grupos, Juntar/Mesclar, modo Teams de redação, instruções e configurações
visuais. Confirmou **Bedrock e Amazon Transcribe**, como serviços distintos,
com opções para comparação. Voxtral e o catálogo AWS atual da origem são
referências, sem dispensar validação de disponibilidade no ambiente futuro.

Excluir do SaaS nesta versão Equipe, áudio do sistema, vídeo, botoneira e
conexões de IA por API genérica/OpenAI/OpenRouter/Azure. A botoneira foi
retirada na correção final, substituindo a indicação inicial de mantê-la.
Teams de redação não é o modo Equipe. Entra ID para autenticação permanece
uma investigação independente da exclusão de Azure como provider de IA.

Trazer **BL-099** como trabalho novo do SaaS: bypass opcional e persistido
do pré-processamento, habilitado por padrão, separado das conversões
necessárias ao reconhecimento e preservando originais. Não implementar
esse item no desktop por consequência desta escolha.

S3 é preferência para arquivos. Validar conta, região, buckets, permissões,
custos e a persistência concorrente de metadados/abas/grupos. Investigar
Whisper no navegador antes de decidir inclusão/fallback e Entra ID no
padrão Orquestra; mock só como possibilidade de desenvolvimento local.
Cada pessoa deve acessar apenas seus próprios recursos no servidor.

O STT Hub recebeu a documentação consolidada e fontes primárias de pesquisa
documental. Há caminhos documentados para Whisper em WASM/WebGPU, mas nenhum
teste de qualidade, hardware ou desempenho foi executado. Sem inferência,
autenticação real, criação de infraestrutura ou scaffold nesta rodada.
Conversa ao vivo, TTS e outros itens não citados continuam sem decisão no
SaaS. O backlog desktop permanece independente; BL-098 segue abandonado.

## Direções ainda sem escopo

Comandos de voz, escrita ditada e automações/integrações continuam como
possibilidades sem lote aprovado. Wake word na Conversa ao vivo está em
BL-097; TTS do texto processado está em BL-089. Esse registro preserva as
direções úteis do plano anterior, sem autorizar pesquisa ou implementação.
