`motor.wav` contains “Hello. This is a short recording.”

The sample was generated locally with Windows System.Speech, using file output
instead of speakers. It is the same synthetic speech as
`tests/fixtures/speech.wav`, converted to 16 kHz, mono, 16-bit PCM with FFmpeg.

The application embeds it for the manual **Testar motor** diagnostic. No
microphone, private recording, text-to-speech installation or AWS request is
needed to run the check. The check uses English for this sample only.
