# STT-widget — Windows

## Abrir e identificar o programa

Extraia todo o ZIP e abra **STT-widget.exe**. Mantenha `tools` e `licenses`
ao lado dos executáveis. Requer Windows 10/11 de 64 bits; Go, GCC e Python
não são necessários para usar o pacote.

Em **Configurações → Sobre**, confira versão, revisão e caminho do
executável aberto. Uma janela já em execução é ativada em vez de duplicada.
Para trocar de versão, feche a anterior antes de abrir a nova.
O CLI `stt-widget-cli.exe` funciona sem abrir a interface.

## Motores e conexões

Em **Configurações → Áudio**, escolha **AWS Cloud**, **Whisper local** ou
**API**. O motor de áudio é independente do provider de texto em **LLM**.

AWS usa perfil/região em **Conexões → AWS**. É preciso ter acesso ao serviço
e modelo escolhidos. A AWS CLI só é necessária quando o perfil a utiliza.
Gravação completa e Stream têm preferências próprias. Transcribe por arquivo
exige um bucket S3 existente na mesma região; a alternativa direta é
escolhida explicitamente, sem apagar o áudio.
O app não cria buckets nem modifica permissões.

Whisper instala motor CPU e pesos GGML nas configurações. Escolha GitHub ou
Hugging Face como fonte; uma falha não muda a fonte automaticamente.
**Testar motor** usa uma amostra sintética e permite cancelar.
Whisper e API não oferecem Transcrição Stream.

Em **Conexões → API**, crie a conexão e salve a chave. Selecione-a em
**Áudio → API** ou **LLM** e escolha o modelo daquele fluxo.
Texto e áudio podem compartilhar uma conexão, mas têm modelos independentes.
Contas no mesmo endpoint podem usar conexões/chaves distintas.
Catálogo disponível não comprova inferência; ID manual permanece acessível.
As chaves ficam no Gerenciador de Credenciais do Windows, nunca nos JSONs.
Azure Speech é experimental; Azure OpenAI/Foundry usa endpoint v1 e deployment.
Esses fluxos exigem recursos e permissões compatíveis já provisionados.

## Gravação, conversa e reprodução

O menu do microfone escolhe **Microfone**, **Sistema** ou **Equipe** e os
dispositivos. Equipe preserva dois WAVs separados. **Atualizar dispositivos**
renova a lista sem trocar fontes de uma captura em andamento.
Pausar exclui o intervalo; Descartar pede confirmação e não transcreve.
Gravar/importar acrescenta ao texto da aba e preserva o processamento.
Cancelar reconhecimento mantém áudio e partes prontas; **Tentar novamente**
retoma pendências. **Retranscrever áudio** inicia uma tentativa nova integral.

Os modos são **Gravação e texto**, **Transcrição Stream** e **Conversa ao vivo**.
Escolher não inicia captura ou envio. A conversa usa somente microfone,
Nova 2.5 Sonic ou Nova 2, perfil/região AWS e histórico próprio no painel
**Resposta**. Voz, autoplay, saída e volume são independentes.
O texto processado reaparece ao sair desse modo.
Bedrock limita a conexão a oito minutos; não há renovação automática.
Outra sessão é iniciada explicitamente e conserva o contexto recente.
Conversa contínua via OpenRouter não está habilitada.

Depois da captura, **Play/Pausa** ou **Ctrl+Shift+Espaço** permite ouvir;
durante a captura, o atalho pausa/retoma a gravação. **Stop** faz o próximo
Play começar do início. Velocidades de **0,5× a 2,5×** preservam o tom;
áudio novo começa em 1×. Volume de reprodução e ganhos de captura são
independentes, de **0 a 200%**; a saída acima de 100% tem limitador suave.
Duplo clique na bolinha restaura 100%.
O histórico de áudio restaura áudio/texto/screenshot/pendências sem transcrever
automaticamente. Importar oferece também **Exportar MP3**, preservando originais.

## Abas, grupos e texto

O + de cada linha cria uma aba daquele grupo; o + à esquerda cria um grupo.
Arraste abas/grupos para reordenar ou mover entre grupos.
Os três pontinhos ou clique direito no nome abrem as ações do grupo,
incluindo renomear e ajustar sua largura. Textos e tarefas pertencem à aba.
Painéis recolhem independentemente e guardam tamanhos/zoom próprios.

**Processar** inicia conversa nova; **Reprocessar** continua a atual.
Livre envia o texto exato; Teams, Mesclar e instruções personalizadas têm
configurações próprias. Modelo/raciocínio de Processar e Mesclar são separados.
As setas consultam o histórico; o lápis permite editar um turno antigo,
exigindo novo envio explícito. O globo controla busca web por aba.
Reconhecer áudio não executa automaticamente os pedidos ditados.

**Juntar** concatena e **Mesclar** usa o modelo, somente no grupo de origem.
Os originais são arquivados antes de substituir/fechar abas.
Desfazer fechamento conserva uma pilha por **30 segundos**, renovada por outro
fechamento dentro do prazo. O prazo corre com o app fechado; reabrir/desfazer
não renova. Desfazer Juntar/Mesclar restaura a operação inteira.
Arquivos excluídos não são recriados pela restauração.

## Capturas, sons e dados

A câmera seleciona uma área sem pausar a gravação. Esc cancela; o original
é salvo antes do editor. Salvar atualiza aquele PNG; fechar mantém a última
imagem salva. Screenshots aparecem na posição correspondente entre as falas.
Copiar/salvar preserva o caminho exato dos links.

O vídeo prepara a área para a próxima captura. **Configurações → Vídeo**
oferece Baixa, Média e Alta; a escolha fica fixa ao iniciar.
A borda roxa fica cinza na pausa, aceita cliques e sai ao encerrar.
O MP4 é separado e não substitui os WAVs originais.

**Painel de sons / Soundboard** tem efeitos prontos e personalizados,
matriz responsiva, posição salva e volume próprio. Um clique substitui o
efeito; o mesmo botão reinicia, sem fila. Não injeta som em microfone nem
em outros aplicativos. Licenças e restrições dos sons acompanham o pacote.
Não presuma permissão de redistribuição de todo asset de terceiros.

**Visual** escolhe tema, layout e idioma da interface, sem traduzir conteúdo
do usuário nem mudar o idioma da transcrição.
**Dados** limpa itens internos por categoria/período com prévia e confirmação,
enviando-os à Lixeira. Itens em uso e arquivos externos/exportados são protegidos.
A avaliação de transcrição usa nota 0–5 e comentário opcional; limpar um
elemento vinculado também remove essa avaliação.

Dados ficam em `%LOCALAPPDATA%\AI-Talker`; `AI_TALKER_DATA_DIR` escolhe outro
local. Capturas novas usam subpastas diárias de `gravacoes`, `screenshots`
e `videos`. Arquivos e sessões anteriores continuam legíveis.
O ZIP portátil deve ser aberto pelo launcher que usa `dados` junto do programa;
suas instruções estão em **COMECE-AQUI.md**.
