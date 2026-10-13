# STT-widget portátil — primeiro uso em outra máquina

O pacote é para **Windows 10/11 de 64 bits**, sem instalação do aplicativo.
Não precisa de Go, Python, GCC ou de instalar FFmpeg separadamente.
Mantenha a pasta `tools` junto dos executáveis.

## Abrir

1. Copie o ZIP portátil para a outra máquina.
2. **Extraia todo o ZIP** para uma pasta em que possa gravar arquivos.
3. Abra **Abrir STT-widget Portable.vbs**.

O launcher abre a interface sem console e usa a pasta **dados**, ao lado do
programa, para preferências, abas, gravações, screenshots e cache. A primeira
extração começa sem dados de usuário. O pacote inclui somente a distribuição
da release, este guia, o launcher e uma amostra de voz sintética.

Abra sempre pelo launcher portátil para continuar usando essa pasta.
Abrir **STT-widget.exe** diretamente usa o diretório padrão
`%LOCALAPPDATA%\AI-Talker`, salvo se houver uma variável
`AI_TALKER_DATA_DIR` já definida no ambiente.

Se a execução de VBScript estiver indisponível, abra PowerShell na pasta
extraída e execute:

```powershell
$env:AI_TALKER_DATA_DIR = Join-Path $PWD 'dados'
Start-Process -FilePath '.\STT-widget.exe' -WorkingDirectory $PWD -WindowStyle Hidden
```

As **chaves de API permanecem no Gerenciador de Credenciais do Windows**,
vinculadas ao usuário, à conexão e ao endpoint. Não acompanham o ZIP nem a pasta dados;
configure-as na outra máquina. Uma nova pasta dados na mesma conta Windows
não remove chaves já salvas.

Este modo facilita executar sem instalar e testar o primeiro uso. Não migra
automaticamente credenciais ou caminhos absolutos de áudios já registrados
no histórico quando a pasta muda de localização.

## Configurar transcrição por API

1. Em **Configurações → Conexões → API**, crie uma conexão compatível com Áudio.
2. Para API compatível, informe a URL base exigida pelo serviço. Os presets
   OpenAI/OpenRouter já têm endereço fixo.
3. Cole a chave e clique em **Salvar chave**.
4. Em **Áudio → API**, selecione essa conexão e use
   **Consultar modelos / testar conexão**, ou informe o ID manualmente.
   Escolha um modelo que aceite transcrição de arquivo.
5. Para a amostra incluída, deixe o idioma em **Automático**.
6. Em **Configurações → Áudio → Transcrever com**, escolha **API**.
7. Salve as configurações.

Consultar modelos testa o catálogo; a transcrição seguinte testa a inferência.
A chamada de transcrição utiliza a conta do provider escolhido.
AWS CLI, perfil AWS e motor Whisper não são necessários para esse fluxo.
Na primeira abertura, o padrão ainda é AWS Cloud; um aviso de perfil AWS
ausente não impede abrir as configurações e selecionar API.

Importe **exemplos\voz-sintetica.wav**. As palavras da amostra são:

> Hello. This is a short recording.

Confira as palavras, o idioma e a conclusão da tarefa; pontuação pode variar.
A amostra permite verificar a configuração antes de testar o microfone físico.

## Configurar texto por API

1. Em **Configurações → Conexões → API**, crie ou selecione uma conexão
   compatível com Texto e salve sua chave.
2. Em **Configurações → LLM**, escolha **API** e essa conexão.
   Texto e áudio reutilizam a chave somente quando usam a mesma conexão;
   contas distintas no mesmo endereço podem ter chaves diferentes.
3. Escolha os modelos de **Processar** e **Mesclar** e o raciocínio compatível.
4. Salve as configurações. Elas se aplicam à aba atual.
5. Digite um pedido curto no editor de Transcrição e clique em
   **Processar texto** para conferir o envio e o resultado.

A configuração de LLM não troca o motor de transcrição de áudio.
Os detalhes dos demais recursos estão em **LEIA-ME.md** no pacote.

## Roteiro de validação

| Conferir | Resultado esperado |
| --- | --- |
| Primeira abertura | Janela utilizável, sem abas/textos do outro computador |
| Catálogo e ID manual | Configuração acessível mesmo quando o catálogo não retorna modelos |
| Áudio sintético por API | Palavras da amostra reconhecidas e tarefa encerrada |
| Texto por API | Resposta aparece somente após clicar em Processar |
| Salvar e reabrir | Provider/modelos e abas permanecem na pasta dados |
| Abrir o launcher duas vezes | A janela existente é ativada, sem duplicar essa sessão |
| Microfone e reprodução | Captura, pausa, encerramento e Play funcionam com o hardware da máquina |
| Sistema/Equipe | Dispositivo de saída e ambas as fontes são acessíveis |
| Screenshot | Seleção, edição, Salvar e Cancelar funcionam nesse desktop |
| Escala do Windows | Menus e configurações cabem na janela e os controles são acessíveis |

Para os caminhos de erro, confira chave ausente/inválida, modelo inválido,
falha de rede e Cancelar. A interface deve continuar utilizável e preservar
o áudio/texto pronto. Faça esses casos separadamente dos testes de sucesso.

Para repetir o primeiro uso, feche essa janela e extraia o ZIP novamente
em outra pasta vazia. Na mesma conta Windows, as chaves do endpoint podem
continuar presentes; uma conta/máquina nova permite testar a ausência delas.

## Registrar o resultado

Anote versão do Windows, escala da tela, versão/revisão do STT-widget,
provider e IDs dos modelos de áudio/texto, dispositivo usado, passos,
resultado esperado e resultado observado. A versão pode ser consultada com:

```powershell
.\stt-widget-cli.exe version
```

O log de inicialização fica em **dados\ai-talker-go.log**. Capture a tela do
erro quando isso ajudar a reproduzir. O registro do teste não precisa incluir
a chave da API.

Este pacote não contém instalador. Um instalador pode acrescentar atalhos e
remoção pelo Windows; o ZIP já permite fazer os testes em outra máquina.
