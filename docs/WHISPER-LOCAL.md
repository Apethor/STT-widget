# Whisper local

AWS Cloud continua sendo o motor inicial de transcrição. O ícone de motor
ao lado das barras oferece **AWS Cloud**, **Whisper local** e **API**.
A seleção é salva nas preferências e vale para os próximos pedidos,
sem interromper uma tarefa em andamento.
Stream fica desabilitado neste motor; Equipe ainda captura microfone e
saída do sistema em fontes separadas e transcreve após parar.

Quando um áudio demorar, **Cancelar transcrição** encerra a inferência local
e preserva o arquivo. **Tentar novamente** permite continuar com esse mesmo
áudio, usando o motor atualmente selecionado. Você pode mudar para AWS Cloud
antes de clicar, sem importar o arquivo novamente. Cache é reaproveitado
somente quando compatível com o modelo e as opções do pedido.

## Configurar pela interface

Escolha **Whisper local** em **Configurações → Áudio → Transcrever com**
ou no menu do motor. Em **Configurações → Whisper**:

1. Use **Baixar motor Whisper** para instalar whisper.cpp.
2. Escolha um modelo e a **Fonte do download do modelo**:
   GitHub ou Hugging Face.
3. Use **Baixar modelo selecionado** quando a combinação estiver habilitada.
4. Ajuste idioma, threads e busca de alternativas.
5. Use **Testar motor** para conferir a instalação e a inferência; salve as configurações.

Os botões de pasta aceitam uma instalação existente de `whisper-cli.exe`
e um arquivo **GGML `.bin`**. Pesos Python `.pt` do OpenAI Whisper não são
compatíveis com esse motor. Ao escolher outro arquivo, o modelo fica como
**Arquivo local (GGML)**.

O download informa progresso, pode ser cancelado e ocorre fora da interface.
Fechar as configurações cancela o download. Os arquivos parciais são removidos;
uma instalação/modelo anterior é preservada se o download falhar.
Bloqueios de rede aparecem como erro e permitem selecionar arquivos existentes.

Depois de instalar motor e modelo, a transcrição usa somente a CPU deste
computador, sem AWS nem rede. O processamento opcional do texto por LLM
usa as escolhas independentes de **Configurações → LLM**, por Bedrock ou API.

GitHub é a fonte inicial. A escolha é exclusiva: uma falha não tenta o
outro site automaticamente. Para tentar outra fonte, altere a seleção e
clique novamente em baixar. A interface informa a fonte e desabilita o
download quando não há URL e checksum configurados para aquela combinação.
Selecionar uma fonte não substitui um modelo ou motor já instalado; um
arquivo existente com hash válido é reaproveitado.

## Testar o motor

**Testar motor** usa as opções atualmente preenchidas, inclusive modelo,
caminhos, threads e busca de alternativas, sem precisar salvá-las primeiro.
Ele confere o executável, o modelo e as DLLs da instalação automática.
Depois executa o motor com uma amostra sintética incluída no aplicativo:
“Hello. This is a short recording.”

O resultado fica **verde** quando a transcrição reconhece a frase e mostra
o tempo gasto. Uma falha fica **vermelha**, com o arquivo ausente ou a etapa
que falhou; saídas vazias ou sem a frase esperada também falham.
É possível corrigir as opções e testar novamente.

O teste ocorre fora do loop da interface, tem limite de três minutos e
oferece **Cancelar teste**. Fechar as configurações também o cancela.
Não grava o microfone, não usa AWS e não baixa arquivos automaticamente.
Somente essa amostra usa inglês e não recebe o glossário pessoal; o idioma
e o glossário das transcrições seguintes permanecem conforme configurados.

## Modelos e opções

Todos os modelos oferecidos são multilíngues. As colunas indicam combinações
de download com URL e checksum fixados no catálogo do aplicativo; o acesso
à rede ainda pode falhar. Um traço indica download desabilitado naquela
fonte, sem afirmar que o modelo não existe em outros sites.

| Modelo | Download aproximado | GitHub | Hugging Face |
| --- | --- | --- | --- |
| Tiny Q5 | 31 MiB | Sim | Sim |
| Tiny | 75 MiB | Sim | Sim |
| Base Q5 | 57 MiB | Sim | Sim |
| Base, seleção inicial | 142 MiB | Sim | Sim |
| Small Q5 | 182 MiB | Sim | Sim |
| Small | 466 MiB | Sim | Sim |
| Medium | 1,5 GiB | — | Sim |
| Large v3 | 2,9 GiB | — | Sim |
| Large v3 Q8 | 1,6 GiB | Sim | — |
| Turbo | 1,5 GiB | Sim | Sim |
| Turbo Q5 | 547 MiB | Sim | Sim |

O tamanho do arquivo não é o uso máximo de memória. Modelos maiores podem
levar mais tempo na CPU e exigir mais memória. As barras indicam o tamanho
do modelo escolhido, não a confiança na transcrição.

**Tiny Q5 e Base Q5** são as alternativas menores para testar quando Large
Q8 estiver lento. Tiny, Base, Small e seus Q5 podem ser baixados pelo GitHub.
GitHub verifica SHA256; Hugging Face usa o SHA1 legado fixado no catálogo.
Medium e Large v3 comuns não têm download GitHub configurado; Large v3 Q8
não tem checksum Hugging Face configurado. Uma combinação desabilitada
permite escolher explicitamente a outra fonte ou um arquivo GGML existente.
Uma opção de arquivo personalizado continua preservada ao atualizar o aplicativo.

**Idioma:** automático, português, inglês, espanhol, francês, alemão,
italiano, japonês ou chinês. Automático é o padrão; o aplicativo não ativa
tradução. O glossário da seção Áudio é enviado como contexto de termos.

**Threads:** 0, o padrão, escolhe até oito threads conforme a CPU; aceita
de 1 a 64 para configuração manual.

**Busca de alternativas:** rápida com uma alternativa, equilibrada com cinco
(padrão) ou dez alternativas. O controle corresponde ao `beam-size` do CLI.
Microfone, divisão em partes, sobreposição e detecção de silêncio continuam
nas opções compartilhadas de áudio.

## Motor e arquivos

O aplicativo baixa o pacote CPU Windows AMD64 do **whisper.cpp 1.9.5**,
build oficial **b5454**, da mesma revisão
`d1be6fde11ac6e0407606b4e42fe72d34add8037` da tag 1.9.5.
Python, pip, CUDA e uma chave de API não são necessários.

URL oficial:
`https://github.com/ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-x64.zip`

SHA256 do ZIP, publicado pelo release:
`6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb`

O instalador extrai somente `whisper-cli.exe`, `whisper.dll` e bibliotecas
`ggml*.dll`, com a licença MIT. Caminhos inválidos, links simbólicos,
duplicatas e pacotes incompletos são rejeitados.

Fontes de modelos GitHub configuradas com SHA256:

- Tiny/Base, comuns e Q5: `https://github.com/ddddwq2q/whisper-models/releases`
- Small/Small Q5/Turbo Q5: `https://github.com/riddl0r/voice-models/releases`
- Large v3 Q8, indicado pelo usuário:
  `https://github.com/sergheinenov/whisper-large-v3-ggml/releases`
- Turbo: `https://github.com/blind0wl/nirivoice-mac-assets/releases`

Os downloads Hugging Face selecionados explicitamente usam o repositório
upstream `https://huggingface.co/ggerganov/whisper.cpp`.
Hashes e URLs de cada arquivo ficam em `internal/whisper/catalog.go`.
Downloads usam HTTPS e redirecionamentos limitados aos hosts GitHub/Hugging Face.
Uma falha não instala conteúdo com checksum diferente nem remove modelos anteriores.

Os arquivos ficam em:

```text
%LOCALAPPDATA%\AI-Talker\whisper\engine-1.9.5\
%LOCALAPPDATA%\AI-Talker\whisper\models\
```

`AI_TALKER_DATA_DIR` também muda esse destino. Preferências usam
`transcription_provider` e `whisper` em `settings.json`; arquivos antigos
sem esses campos continuam em AWS Cloud. A fonte do modelo é salva
separadamente em `whisper_download_source`, com GitHub como padrão quando
o campo está ausente.
Uma instalação automática 1.9.2 continua funcionando até instalar o motor novo.
Um bloqueio temporário do Windows na publicação de executáveis recebe
retentativas curtas; cancelar interrompe a espera e preserva a versão anterior.

## Execução e cache

FFmpeg prepara WAV mono de 16 kHz, 16 bits, com o mesmo pré-filtro suave
de 80 Hz a 7 kHz usado no envio ao Bedrock. O WAV evita a compressão MP3.
A detecção local de silêncio evita iniciar o modelo
desnecessariamente. O CLI recebe caminhos e opções como argumentos,
sem shell; processos auxiliares permanecem ocultos.

O serviço compartilha uma única vaga de inferência local para evitar vários
modelos carregados simultaneamente. Cada parte tem prazo de trinta minutos
e herda o cancelamento da tarefa. Cancelar encerra o processo e remove
temporários. Arquivos gravados e partes concluídas no cache permanecem.

O cache distingue os motores de transcrição, modelo, idioma, opções,
glossário e conteúdo do áudio. Também inclui o SHA256 dos arquivos reais
do motor e dos pesos: substituir um GGML no mesmo caminho não reutiliza
resultados antigos.
A fonte de download fica fora da identidade de reconhecimento: trocar
GitHub por Hugging Face sem alterar os pesos, o motor ou as opções não
invalida o cache. O hash Bedrock anterior permanece compatível.

Referências primárias: README, licença, `models/README.md` e
`examples/cli/cli.cpp` da tag 1.9.5, em
`https://github.com/ggml-org/whisper.cpp/tree/v1.9.5`.
