# Reprodução, histórico e organização das capturas

A lista de velocidades é **0,5×, 0,75×**,
1×, 1,25×, 1,5×, 2×, 2,25× e 2,5×. As opções lentas também preservam o
tom: a preparação usa `atempo` em uma cópia, mantendo os arquivos originais.
Um áudio novo começa em 1×; Stop conserva a escolha do mesmo áudio.

O botão de pausa do gravador mostra Play quando a captura terminou
e há áudio carregado. Clique para ouvir, pausar, retomar e ouvir novamente
depois do fim. O atalho Ctrl+Shift+Espaço mantém essas mesmas ações.
Durante a gravação, o botão continua pausando e retomando a captura.
Sem áudio e sem captura, o botão fica oculto.

Clique ou arraste na onda para avançar ou voltar durante a reprodução.
O contador acompanha a posição pedida; se estava pausado, continua pausado.
Selecionar o fim conclui a reprodução e permite começar outra vez.

Enquanto toca ou está pausado, **Stop** encerra a reprodução; o próximo
Play começa do início. Play/Pausa continua sendo um único botão, com ícone
e tooltip atualizados para ouvir, pausar ou retomar do ponto interrompido.

Com áudio carregado, um pequeno ícone de alto-falante abre o **volume da
reprodução** de 0 a 200%. A barra aparece somente ao clicar; o ajuste funciona
durante Play e Pausa e é salvo ao soltar. Falha de salvamento restaura o
volume anterior. O ganho é aplicado à cópia privada de reprodução; acima de
100%, um limitador suave reduz a saturação. A sessão CoreAudio do aplicativo
permanece em 100% e o arquivo original não é alterado. A botoneira tem sua
própria cópia e volume, sem acompanhar o slider do player.
Microfone e sistema mantêm os controles
independentes de entrada, de 0 a 200%, no popup do botão **Volume da captura**.
Esse botão fica entre **Microfone** e **Stream** na linha de atalhos,
tanto em Médio como em Grande.
Duplo clique **na bolinha** de qualquer um desses sliders restaura **100%**
e aplica/salva o valor como um ajuste normal. Duplo clique fora da bolinha
mantém o valor. O volume global do Windows permanece fora desse fluxo.

Ao lado da onda, o controle de **velocidade** oferece **0,5×, 0,75×, 1×,
1,25×, 1,5×, 2×, 2,25× e 2,5×**. A preparação pelo FFmpeg preserva o tom e aplica a
velocidade somente à reprodução. Cada áudio novo começa em 1×; Stop mantém
a escolha para ouvir novamente o mesmo áudio. O contador e o seek continuam
referidos ao tempo do áudio original. Mudar velocidade ou ganho mantém a
pausa e a posição escolhida, inclusive durante a preparação da cópia.

A reprodução acompanha a aba e o arquivo carregado. Trocar de aba, fechar
essa aba ou a janela, importar outro áudio, Novo áudio e começar outra
gravação encerram o som. A nova captura espera a saída anterior ser liberada.
Limpar texto e cancelar uma transcrição mantêm a reprodução independente.
O contador mostra a posição enquanto se ouve e restaura a duração anterior
depois; essa posição temporária não é persistida como duração da aba.

O player usa WinMM/MCI em uma goroutine com contexto próprio. Preparação,
decodificação e controle do dispositivo ficam fora do loop da interface.
WAVs do gravador usam uma cópia privada, feita com memória limitada.
Outros formatos são decodificados pelo FFmpeg que acompanha o pacote.
Em Equipe, as duas fontes são reunidas para ouvir, com seus deslocamentos
originais e normalização contra saturação. As trilhas originais continuam
separadas para transcrever. O WAV temporário é removido após terminar,
cancelar ou falhar. Erros de reprodução permitem tentar novamente.
Os comandos nativos ficam no mesmo thread do player. Caminhos muito longos
usam uma cópia privada em uma localização temporária mais curta, removida
pelo mesmo fluxo; o original permanece intacto.

## Exportação

Com áudio carregado, **Importar áudio** abre um menu com **Importar** e
**Exportar MP3**. Sem áudio, continua abrindo diretamente o seletor de importação.
A exportação usa **192 kb/s**, estéreo a 48 kHz, e a mesma mistura da reprodução.
Equipe exporta microfone e sistema juntos, respeitando seus deslocamentos.
O volume de saída não altera o arquivo exportado.

Escolha o destino no seletor nativo. O arquivo final só é substituído depois
de uma codificação bem-sucedida; erro/cancelamento mantém o destino anterior.
Os WAVs, manifestos e arquivos importados permanecem preservados.

## Histórico de áudio por aba

No gravador, o botão **Histórico de áudios desta aba** abre uma lista das
fontes usadas, da mais recente para a mais antiga, com nome e duração quando
disponível. O áudio atual aparece marcado. O botão fica disponível quando
há histórico e a captura terminou.
A lista usa páginas de dez áudios, com **Mais antigos…** e **Mais recentes…**
para acessar todas as entradas dentro da janela mínima.

Antes de gravar/importar outra fonte, usar Novo áudio ou carregar uma entrada
anterior, o app preserva o estado do áudio atual. Escolher uma entrada restaura
o arquivo, a transcrição acumulada correspondente, a referência usada para
acrescentar/substituir seu trecho, a última tentativa, a linha do tempo e os
links de screenshots. Pendências de Equipe/Stream e trechos já aceitos
acompanham essa entrada; os outros áudios continuam no histórico.

O texto atual fica preservado antes da troca. O resultado processado, suas
opções e a conversa permanecem na aba. Trocar encerra a reprodução e cancela
o reconhecimento atual, rejeitando suas respostas atrasadas.
Se o texto for editado enquanto o arquivo anterior é localizado, a troca
é cancelada e a edição permanece no áudio atual. Desfazer uma limpeza que
carrega outro áudio também preserva o áudio que estava aberto no histórico.
Screenshots cuja seleção termina depois da troca acompanham seu áudio de
origem; excluir uma captura também retira o link das entradas do histórico.
Durante Retranscrever, a screenshot atualiza somente as capturas do texto
anterior, até as novas falas ficarem prontas. Isso também vale após reabrir
uma tentativa pendente. Limpar mantém o editor vazio ao excluir capturas.
Carregar **não começa outra transcrição**: clique em **Play** para ouvir ou
em **Tentar novamente** para retomar pendências com as escolhas atuais.
**Retranscrever áudio** continua oferecendo uma tentativa nova integral.

O histórico é salvo em `workspace-go-v1.json`, junto da aba, e aceita sessões
anteriores sem esse campo. Ele preserva os áudios usados pela aba a partir
dessa versão; outros arquivos antigos continuam acessíveis por Importar áudio.
Os arquivos e as trilhas originais permanecem nos caminhos salvos.
Se uma fonte foi removida ou falhar a gravação da troca, o app mantém o
estado carregado e mostra um erro recuperável.
Uma retomada pode aguardar a tentativa anterior salvar seu cache; essa espera
também aceita Cancelar. Consulte [a recuperação](RETENTATIVAS-TRANSCRICAO.md).

## Pastas das capturas

Novas gravações usam `gravacoes/AAAA-MM-DD`, resolvida ao iniciar a captura
pela data local; todas as fontes de Equipe recebem essa mesma pasta.
Passar da meia-noite mantém o WAV em sua pasta original. Screenshots usam
`screenshots/AAAA-MM-DD`, resolvida junto com a data/hora do nome do PNG.
Salvar marcações substitui o mesmo arquivo dentro dessa pasta.
Não há migração automática de arquivos existentes. Links registrados
aceitam a pasta antiga e as novas subpastas de data.

WAVs novos usam `AAAAMMDD-HHMMSS.wav`, com `-mic`/`-sis` em Equipe
e sufixo numérico se houver colisão. O nome aparece acima do relógio;
o pequeno botão ao lado copia o caminho, ou ambos os WAVs de Equipe.
O botão de pasta oferece **Gravações**, **Screenshots** e **Capturas de vídeo**.
O histórico de áudio é independente da navegação por prompts/respostas de
[Processar texto](PROCESSAMENTO-TEXTO.md).

## Verificação

Os testes usam captura, saída e transcrição simuladas para conferir estados,
isolamento, cancelamento e ordem de liberação. A integração de codecs verifica
MP3, duas fontes sobrepostas, deslocamento e cobertura da fonte mais longa,
mantendo bytes dos originais. O teste nativo opcional do Windows reproduz
somente silêncio:

Execute o exemplo a partir da raiz do repositório.

```powershell
. .\scripts\go-env.ps1
$env:AI_TALKER_PLAYBACK_TEST = '1'
try {
    go test -mod=readonly -tags=integration -race -v -run '^TestWindows(Playback(Silent|SessionVolume)Integration|ConcurrentSilentPlayersKeepIndependentControl)$' ./internal/playback
} finally {
    Remove-Item Env:AI_TALKER_PLAYBACK_TEST -ErrorAction SilentlyContinue
}
.\scripts\smoke-window.ps1 -PlaybackOnly
.\scripts\smoke-window.ps1 -BacklogOnly
```

O smoke de interface usa um player sintético sem saída sonora. Verifica
os cliques no botão visível, pausa da captura, Play, pausa/retomada da
reprodução, volume compacto/persistido, Stop/reinício, fim, nova reprodução,
Novo áudio, WAV na pasta do dia e restauração das duas gravações da aba.
