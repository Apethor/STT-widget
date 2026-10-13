# STT-widget

Leia [HANDOFF.md](HANDOFF.md) ao retomar o trabalho e
[README.md](README.md) para preparar ou gerar uma versão.
Esta cópia foi reduzida aos fontes Go, testes/assets e documentação atual.
Não há instalação, ferramentas locais nem artefatos preservados.
Não reinstale, compile ou publique apenas por encontrar instruções de build.

## Escopo de trabalho

Novas sugestões devem ser registradas em [docs/BACKLOG.md](docs/BACKLOG.md).
O usuário escolhe o lote e autoriza pesquisa, implementação e entrega.
Registrar um pedido não autoriza iniciar essas atividades.
O backlog atual tem BL-081 a BL-101; BL-098 foi abandonado e não deve ser
pesquisado nem implementado. Não reutilize os IDs anteriores.

A limpeza dos fontes/documentos não autoriza implementar esse backlog.
O aceite funcional pertence ao usuário, não aos testes nem a um manifesto.

## Código e geração

Aplicativo Windows AMD64 em Go/Fyne, com WASAPI/WinMM, WebRTC VAD, FFmpeg
e AWS SDK for Go v2. Interface e CLI compartilham `internal/transcript`.
`cmd/ai-talker` e `cmd/ai-talker-gui` são entradas necessárias, não artefatos.
Versões e dependências estão em `go.mod`, `go.sum` e `scripts/toolchain.json`.

| Local | Responsabilidade |
| --- | --- |
| `internal/desktop` | Interface, editores, gerações, grupos, sessões e instância única. |
| `internal/recorder`, `internal/playback`, `internal/audio` | Captura, reprodução, codecs e detecção de fala. |
| `internal/live`, `internal/transcript` | Linha do tempo, cache, recuperação e exportações. |
| `internal/cloud`, `internal/realtime` | AWS e protocolo de conversa contínua. |
| `internal/api`, `internal/apiconfig`, `internal/secrets` | HTTP, conexões e credenciais Windows. |
| `internal/whisper`, `internal/processing` | Reconhecimento local e processamento de texto. |
| `internal/config`, `internal/store`, `internal/version` | Preferências, persistência e identidade do executável. |
| `internal/platform`, `internal/screenshot`, `internal/video` | APIs nativas e capturas da tela. |
| `internal/soundboard`, `internal/branding`, `internal/locale` | Sons, identidade visual e traduções. |
| `scripts`, `tests/fixtures` | Geração, validação e fixtures sintéticas. |

Preserve assets incorporados por `go:embed`, ícones, traduções e licenças.
Áudios dessas pastas integram o código; não são dados descartáveis do usuário.
Não recupere o legado Python ou cópias de fontes antigos sem um pedido.

Comandos, a partir da raiz:

```powershell
.\scripts\prepare-go.ps1
.\scripts\test-go.ps1 -Integration
.\scripts\build-go.ps1 -Package
.\scripts\iniciar.ps1
```

Execute somente os comandos autorizados pelo trabalho atual.
`prepare-go.ps1` recria `artifacts` e seu módulo isolado, baixa ferramentas
com hashes fixados e não instala Go/GCC globalmente.
Para comandos Go diretos, carregue `. .\scripts\go-env.ps1` na mesma sessão.
Os launchers de desenvolvimento usam `bin`; pacotes portáteis recebem ZIP
e SHA256 explícitos.

## Implementação

Leia os módulos envolvidos e procure helpers existentes antes de alterar
comportamento. Faça mudanças completas e focadas, mantendo padrões,
tipos, nomes e localização. Não use erros ignorados, fallbacks silenciosos
ou sucesso aparente para encobrir uma falha.

Mantenha trabalho demorado fora do loop Fyne. Use `context.Context`,
cancelamento propagado e goroutines com dono e saída definidos.
Callbacks devem validar a aba/geração antes de publicar resultados.
Recursos COM/dispositivos nativos respeitam a thread proprietária.
Processos auxiliares Windows permanecem ocultos, inclusive credenciais.

Textos novos da interface precisam de tradução em `internal/locale/en.json`,
com parâmetros de formatação preservados. Não traduza texto do usuário,
IDs, caminhos, nomes personalizados ou mensagens brutas dos serviços.
O idioma da interface não muda o idioma do reconhecimento.

Diálogos Fyne devem usar Show antes de Resize. A dimensão InputBorder dos
editores permanece positiva porque também define a largura do cursor.
Menus, tooltips e configurações não devem aumentar a janela.
Use o seletor nativo de arquivos e preserve o SVG/ícones originais.

Use `gofmt`, tratamento explícito de erros, `go vet`, testes e detector de
corridas conforme a mudança e o ambiente autorizado. Serviços rotineiros
são simulados, com fixtures sintéticas e diretórios isolados.
Para dados reais, use `AI_TALKER_DATA_DIR` separado.
Não copie chaves ou dados pessoais para logs, código, commits ou pacotes.

Skills da comunidade são orientação, não substituem contratos do aplicativo
nem documentação compatível com a versão Go.
O registro útil está em [docs/GO-SKILLS.md](docs/GO-SKILLS.md).

## Contratos obrigatórios

Gravação exclui pausas. Gravar/importar acrescenta transcrição e conserva
edições e processamento; silêncio mantém o texto anterior.
Retranscrever cria uma tentativa nova; Tentar novamente retoma pendências
da tentativa persistida. Descartar exige confirmação, não transcreve e
preserva textos/arquivos. Limpar durante captura não para a gravação.

Cancelar libera prontamente a UI, interrompe esperas e impede novos envios.
Uma chamada Cloud já enviada pode concluir seu cache, mas não atualizar
outra geração. Whisper cancela também o processo. Retomar pode aguardar
cancelavelmente o cache anterior; não remova locks de outro proprietário.
Falha de escrita não conta como parte concluída nem aciona fallback.
Preserve cobertura até a última amostra, originais e textos parciais.
Detalhes e limites: [recuperação](docs/RETENTATIVAS-TRANSCRICAO.md).

Equipe conserva microfone e sistema em WAVs separados e seus deslocamentos.
Não invente intercalação sem timestamps nem identidade de pessoas pelo texto.
Screenshots não contam como áudio concluído; falhas não contam como sucesso.
Stream fixa o modelo ao iniciar; gravação completa usa a seleção própria.
Modo/dispositivos ativos ficam congelados durante a captura.
Detalhes: [gravador](docs/GRAVADOR-INTERFACE.md) e
[Stream/Equipe](docs/STREAM-EQUIPE-CONTEXTO.md).

Cada aba isola tarefas, áudio, textos, opções, erros e históricos.
Trocar de aba não redireciona callbacks. Tamanhos/recolhimentos pertencem
à aba; abas novas herdam os da última ativa. Grupos persistem e possuem sua
linha, nomes e limites de largura. Fechar conteúdo pede confirmação e
preserva rascunhos; limpar todos mantém uma única aba vazia.

Desfazer fechamento é uma sequência persistida por **30 segundos**,
renovada por novo fechamento de aba/grupo dentro do prazo.
Abrir ou desfazer não renova; o prazo corre com o app fechado.
Grupo removido pela última aba e Juntar/Mesclar são restaurados integralmente.
Fechar/limpar explicitamente um grupo descarta suas entradas.
Restauração não reinicia tarefas nem recria screenshots excluídas.
Detalhes: [Desfazer persistido](docs/DESFAZER-PERSISTIDO.md).

Processamento começa somente por ação explícita. Livre envia o texto exato;
Teams respeita orientações ditadas. Escolher instrução não muda provider/modelo.
Processar abre conversa nova; Reprocessar continua com contexto concluído
e última resposta editada. Novo áudio preserva esse contexto.
Histórico antigo exige lápis e novo envio explícito com opções atuais;
turnos posteriores não são recalculados. Falha/cancelamento não cria turno.
Busca web só atende tool calls do pedido atual e não finge fontes.
Juntar/Mesclar afetam somente o grupo de origem e preservam originais antes
do sucesso. Detalhes: [processamento](docs/PROCESSAMENTO-TEXTO.md).

Reprodução é independente da transcrição e usa cópias; Stop faz o próximo
Play começar do início. Trocar/fechar aba, Novo áudio, importação e nova captura
encerram o player. Ganhos de captura e reprodução são independentes.
Velocidade preserva o tom; ganho acima de 100% usa limitador suave.
Carregar histórico restaura áudio/texto/pendências, sem transcrever sozinho.
Detalhes: [reprodução e histórico](docs/REPRODUCAO-AUDIO.md).

Screenshots não pausam gravação. Esc não deixa PNG/editor; Salvar altera
somente a captura daquele editor. Marca temporal é reservada ao clicar,
antes do arraste e descontando pausas. Seleção tardia conserva áudio/aba
de origem; excluir remove também referências sem republicar falas antigas.
Leitura compacta links só na apresentação; edição/cópia/exportação preserva
texto e URLs exatos. Detalhes: [screenshots](docs/SCREENSHOTS.md) e
[links](docs/LINKS-RESULTADO.md).

API texto/áudio tem modelos independentes e segredos vinculados à conexão/
endpoint no Gerenciador de Credenciais Windows, nunca em JSON.
Não siga redirects autenticados. HTTP só é aceito em loopback para Genérico;
OpenAI/OpenRouter usam endpoints fixos.
Dados legados continuam legíveis; incompatibilidade exige correção explícita.
Detalhes: [APIs](docs/APIS-PROVIDERS.md).

Limpeza de dados mostra categoria/período, quantidade/espaço e confirmação,
envia itens internos à Lixeira e protege originais externos/exportados.
Áudio removido mantém texto/histórico com marca de indisponível; transcrição
removida mantém o áudio. Itens em uso não são apagados.
Avaliação é nota 0–5, diferente de sem avaliação, com comentário opcional.
Reavaliar substitui ambos; confirmar sem estrelas salva zero.
O registro conserva etapas/prompts/textos finais e vínculos de áudio/captura,
sem incluir Processar/Mesclar. Fechada a aba, fica só para diagnóstico.
Limpar qualquer elemento vinculado, inclusive screenshot, remove a avaliação.

Conversa ao vivo usa somente microfone, contexto e histórico próprios,
sem substituir o processamento anterior ao trocar de modo.
Não simule protocolo contínuo com STT+TTS nem habilite um provider sem
contrato comprovado. Interrupção/Stop descartam fila e preservam os WAVs.
Painel de sons prepara efeitos, substitui/reinicia sem fila e possui volume
independente. Detalhes: [conversa](docs/SPEECH-TO-SPEECH.md) e
[sons](docs/SOUNDBOARD.md).

Conferências nativas precisam verificar os controles e o executável real,
incluindo Sobre, aparência e caminhos de erro. Não trate testes headless
ou evidências de uma versão anterior como aceite de uma nova entrega.
