# Pré-filtro para transcrição

A preparação conserva áudio mono a **16.000 amostras por segundo**. Isso
representa frequências de até 8 kHz; **64 kb/s** é a taxa de dados do MP3
enviado ao Bedrock, não uma frequência de amostragem.

O FFmpeg reamostra para 16 kHz e aplica filtros suaves passa-altas de **80 Hz**
e passa-baixas de **7 kHz** antes da detecção de fala. As frequências dos
filtros são pontos de atenuação de 3 dB, não cortes absolutos. O mesmo sinal
preparado vai ao MP3 do Bedrock ou ao WAV do Whisper local. O arquivo original
continua preservado.

A faixa foi escolhida de modo conservador. A recomendação ITU-T G.722 define
áudio de banda larga de 50 Hz a 7 kHz, enquanto a faixa telefônica de 300 Hz
a 3,4 kHz é mais estreita. Estudos de percepção mostram que frequências acima
de 4 kHz ainda podem fornecer pistas para distinguir consoantes. O Whisper
usa 16 kHz em seu pré-processamento. Essas referências justificam conservar
a banda alta de voz, mas **não demonstram que o novo filtro melhora a taxa
de acerto do Voxtral ou do Whisper**. Isso requer comparação com gravações
representativas, seus textos corretos e a versão anterior.

A mudança invalida o cache de partes preparado antes do filtro. Importações
de 8 kHz são reamostradas antes dos filtros, para permanecerem aceitas.

Referências:

- [ITU-T G.722, sistema de áudio de 50 Hz a 7 kHz](https://www.itu.int/rec/T-REC-G.722)
- [ITU-T P.310, faixa telefônica de 300 Hz a 3,4 kHz](https://www.itu.int/rec/T-REC-P.310)
- [OpenAI Whisper, parâmetros de áudio](https://github.com/openai/whisper/blob/main/whisper/audio.py)
- [Apoux e Healy, importância das faixas de frequência para vogais e consoantes](https://pmc.ncbi.nlm.nih.gov/articles/PMC3427368/)
- [Monson et al., energia de alta frequência na percepção da voz](https://pmc.ncbi.nlm.nih.gov/articles/PMC4059169/)
- [Documentação dos filtros de áudio do FFmpeg](https://ffmpeg.org/ffmpeg-filters.html)
