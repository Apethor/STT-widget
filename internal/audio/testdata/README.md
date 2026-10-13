# Áudio de teste

`speech.pcm` e `short-word.pcm` são conversões dos WAVs sintéticos versionados
em `tests/fixtures`. Formato: PCM assinado de 16 bits little endian, mono,
16.000 amostras por segundo. Não contêm áudio do usuário.

Conversão com o FFmpeg 9.0.2 fixado em `scripts/toolchain.json`:

```powershell
ffmpeg -i tests/fixtures/speech.wav -ac 1 -ar 16000 -f s16le internal/audio/testdata/speech.pcm
ffmpeg -i tests/fixtures/short-word.wav -ac 1 -ar 16000 -f s16le internal/audio/testdata/short-word.pcm
```

Os testes unitários leem PCM diretamente, sem executar um codec externo.
O teste com `-tags=integration` valida o decodificador e o MP3 separadamente.
