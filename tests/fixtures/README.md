The first WAV fixtures were generated locally with Windows System.Speech, with output
directed to files rather than the speakers:

- `speech.wav`: “Hello. This is a short recording.”
- `short-word.wav`: “Hi.”

`stream-portuguese.wav` uses eSpeak NG 1.52.0, voice `pt-br`, speed 145, with
output directed to a file and converted to mono PCM 16 kHz with FFmpeg. Its text:

> Hoje eu vou estudar programação. Quero salvar a imagem da tela e continuar
> explicando o exercício. Esta gravação deve permanecer em português.

They provide audible speech for regression tests without using private recordings,
calling a transcription service, or requiring text-to-speech during the tests.
