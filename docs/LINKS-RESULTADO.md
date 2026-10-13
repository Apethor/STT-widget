# Links na transcrição e no resultado processado

Resultados com links HTTP/HTTPS abrem automaticamente numa visualização de
leitura dentro do box Processar texto. O botão **Editar resultado** volta ao
editor; nele, o botão **Ler resultado com links clicáveis** reabre a leitura.
Essa escolha é independente por aba. Ao reabrir uma sessão com links salvos,
a leitura reaparece.

Links Markdown mantêm o título: `[Documentação](https://example.com/...)`
aparece como **Documentação**. URLs soltas, entre parênteses, colchetes ou
delimitadas por `< >` aparecem com o nome do site. A URL completa, seus
parâmetros e fragmento continuam sendo o destino do clique. Pontuação depois
da URL fica fora do destino; parênteses balanceados em caminhos são preservados.

Cliques abrem no navegador padrão do Windows com `ShellExecuteW`, executado
fora do loop da interface. Não há console nem interpretação como comando.
Erros ao abrir o navegador aparecem sem modificar o texto ou bloquear o box.

O `widget.Entry` continua sendo a origem para edição, rascunhos, sessão,
copiar e salvar. A compactação é somente visual e não reescreve respostas.
Editar durante um processamento continua rejeitando a resposta antiga.
Código inline e blocos de código não viram links; imagens Markdown viram
links sem baixar a imagem ao exibir o texto. Protocolos locais ou de apps
não são clicáveis, exceto screenshots registradas pelo próprio aplicativo.

A transcrição usa a mesma alternância entre leitura e edição. Capturas
iniciadas durante uma gravação aparecem na posição correspondente da fala
como links `Screenshot 1`, `Screenshot 2` etc. Sem Stream, essa montagem acontece
ao concluir o reconhecimento pelo Bedrock ou Whisper; em Stream, durante a
captura. O clique abre a imagem no visualizador padrão do Windows;
copiar e salvar incluem o caminho completo de cada arquivo. A sessão guarda
os vínculos, e Juntar/Mesclar os preservam no destino. Consulte
[SCREENSHOTS.md](SCREENSHOTS.md) para os detalhes.

`internal/desktop/links.go` usa o parser Markdown da versão Fyne fixada e
converte URLs em segmentos de hyperlink. A abertura nativa e a validação
de páginas ficam em `internal/platform/url*.go`. Não há novas dependências.
A leitura usa rolagem externa ao RichText do Fyne 2.8.1 para
atualizar os destinos reais dos cliques junto do texto exibido.

Os testes simulam o navegador e verificam nomes, destino exato, query/fragmento,
parênteses e colchetes, código, imagens sem carregamento automático, copiar
sem perder URLs, alternância entre leitura e edição e isolamento entre abas.
Também tocam os hyperlinks renderizados depois de trocar o texto, para
evitar abrir um endereço ou uma imagem da resposta anterior.
