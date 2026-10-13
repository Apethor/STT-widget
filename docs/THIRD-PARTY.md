# Componentes de terceiros

O código original do STT-widget usa a [licença MIT](../LICENSE).
Essa licença não substitui os termos próprios dos componentes e assets abaixo.

As versões exatas do código Go estão registradas em `go.mod` e `go.sum`.
Os componentes abaixo não são código original do STT-widget.

| Componente | Uso | Licença / fonte |
| --- | --- | --- |
| Go | Compilação e runtime | BSD; https://go.dev/LICENSE |
| Fyne | Interface desktop | BSD-3-Clause; https://github.com/fyne-io/fyne/blob/v2.8.1/LICENSE |
| AWS SDK for Go v2 e Smithy Go | AWS, credenciais, Bedrock, Transcribe e S3 | Apache-2.0; https://github.com/aws/aws-sdk-go-v2 |
| whisper.cpp 1.9.5 e GGML | Transcrição local opcional pela CPU, baixada pelo configurador | MIT; https://github.com/ggml-org/whisper.cpp/tree/v1.9.5 |
| golang.org/x/sys e golang.org/x/text | Windows, Unicode | BSD; https://cs.opensource.google/go |
| go-webrtcvad e WebRTC VAD | Detecção local de fala | MIT para o wrapper e BSD para WebRTC; https://github.com/maxhawkins/go-webrtcvad |
| FFmpeg 9.0.2 essentials, build Gyan | Decodificação, conversão e MP3 em processo separado | GPL, conforme licença incluída no arquivo original; https://github.com/GyanD/codexffmpeg/releases/tag/9.0.2 |
| WinLibs GCC 16.2.0 / MinGW-w64 | Compilação C no desenvolvimento | GPL com exceção de runtime / licenças MinGW; https://winlibs.com |

FFmpeg/FFprobe são executáveis separados, sem ligação de suas bibliotecas ao
binário Go. O pacote conserva a licença do build original. A revisão fonte
informada pelo fornecedor é `946fcce07b`, no repositório
https://github.com/FFmpeg/FFmpeg. Os fontes correspondentes e instruções de
compilação podem ser obtidos no release do fornecedor. O pacote de aplicação
não distribui o compilador GCC.

As dependências transitivas de Fyne e do SDK conservam suas licenças originais.
O script de distribuição copia os avisos de licença dos módulos resolvidos
para `licenses/go-modules` antes de gerar o ZIP.

O motor whisper.cpp é um download opcional separado. A licença MIT acompanha
o motor instalado e fica também em `licenses/whisper.cpp-LICENSE.txt` no
pacote. A fonte fixada está na revisão `d1be6fde11ac6e0407606b4e42fe72d34add8037`,
compartilhada pelo build `b5454` e pela tag `v1.9.5`. Os pesos GGML são
baixados separadamente pelo GitHub, com checksums fixados, ou pelo repositório
`ggerganov/whisper.cpp` no Hugging Face.
Consulte `docs/WHISPER-LOCAL.md` para os hashes e o formato aceito.

Os efeitos do Painel de sons têm origens e condições próprias, registradas
em `internal/soundboard/assets/SOURCES.md`. O pacote conserva esse registro
em `licenses/soundboard-SOURCES.md`, incluindo hashes, transformações e a
distinção entre as amostras CC0 e os efeitos produzidos
localmente. A licença das bibliotecas Go não se aplica automaticamente
a essas amostras.

Impulso e Brilho substituem os dois excertos Nintendo anteriores por
sínteses originais dedicadas ao domínio público sob CC0 1.0. Os áudios
Nintendo não estão nesta árvore de fontes e não integram os pacotes novos.
