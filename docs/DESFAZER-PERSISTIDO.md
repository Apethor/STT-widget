# Desfazer fechamento persistido

O prazo é de **30 segundos**, conservado ao reabrir e renovado quando outra
aba ou grupo é fechado dentro do período.

Cada sequência de fechamentos compartilha um prazo absoluto. Um novo
fechamento legítimo, inclusive de uma aba vazia, renova 30 segundos para
as entradas ainda válidas. Clicar em Desfazer consome a entrada mais recente
sem renovar as demais. Fechar o aplicativo não cria um fechamento de aba.

Ao fechar um grupo, o app conserva o comportamento anterior: remove as
entradas ligadas àquele grupo e renova as entradas restantes de outros
grupos. Não há uma nova operação de Desfazer Fechar grupo. Um grupo removido
ao fechar sua última aba ainda volta pelo Desfazer dessa aba.
Juntar e Mesclar continuam sendo operações recuperáveis inteiras.

O prazo corre enquanto o aplicativo está fechado. Uma sessão reaberta dentro
do prazo oferece apenas o tempo restante; uma sessão reaberta no prazo exato
ou depois dele não oferece as entradas expiradas.

## Persistência compatível

`workspace-go-v1.json` mantém `version: 1` e recebe o campo opcional
`closed_tabs`. Sessões antigas que não contêm esse campo continuam válidas.

Cada entrada guarda:

- `until`: instante absoluto em UTC, sem reinício na abertura;
- `state`: snapshot completo da aba;
- `index`, `group`, `group_index`, `group_name`, `group_automatic_name`,
  `group_name_width` e `group_removed`: posição e identidade do grupo;
- `replacement` e `replacement_untouched`: identidade da substituta e
  proteção do trabalho que ela recebeu;
- `combined` e `label`: snapshots originais de Juntar/Mesclar.

Os snapshots conservam transcrição, resultado manual, prompts e respostas
enviados, histórico de áudio, conversa de voz, marcas de áudio indisponível,
screenshots e tamanhos/zoom dos painéis. Os marcadores dos nomes automáticos
continuam distintos dos nomes personalizados.

Abas abertas e entradas recuperáveis usam um mapa comum de IDs durante a
restauração. Isso conserva referências compartilhadas entre combinações
sucessivas e substitutas. Os snapshots antigos de uma combinação permanecem
separados do estado atualmente aberto.

A hidratação reconstrói dados e controles; não restaura tarefas, contextos
cancelados, captura nem reprodução ativa. Abrir o aplicativo ou consultar
Desfazer não envia áudio/texto para serviços.

O fechamento e o consumo de Desfazer gravam a fila sucessora na mesma
operação atômica que grava as abas. Uma falha de escrita mantém os originais,
a fila e o prazo anteriores. Entradas expiradas não são hidratadas nem
impedem a leitura das abas abertas por um snapshot antigo desnecessário.
Dados recuperáveis ainda ativos são validados antes de alterar a interface.

Limpar/excluir uma screenshot ou um áudio atualiza também os snapshots
recuperáveis. O Desfazer conserva essas remoções e não recria arquivos.
Limpar ou fechar explicitamente um grupo descarta as entradas que
recuperariam seu conteúdo.
