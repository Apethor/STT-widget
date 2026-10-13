package cloud

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"ai-talker/internal/audio"

	"github.com/aws/smithy-go"
)

type ownedCloudClient struct {
	closeHTTP func()
	once      sync.Once
}

func (c *ownedCloudClient) Close() error {
	c.once.Do(func() {
		if c.closeHTTP != nil {
			c.closeHTTP()
		}
	})
	return nil
}

func speechPCM(chunk audio.Chunk) ([]byte, error) {
	data := chunk.Audio
	if chunk.Format != audio.FormatWAV || len(data) <= 44 ||
		len(data) > 44+300*audio.SampleRate*audio.SampleBytes ||
		string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " ||
		string(data[36:40]) != "data" || binary.LittleEndian.Uint32(data[16:]) != 16 ||
		binary.LittleEndian.Uint16(data[20:]) != 1 || binary.LittleEndian.Uint16(data[22:]) != 1 ||
		binary.LittleEndian.Uint32(data[24:]) != audio.SampleRate ||
		binary.LittleEndian.Uint32(data[28:]) != audio.SampleRate*audio.SampleBytes ||
		binary.LittleEndian.Uint16(data[32:]) != audio.SampleBytes ||
		binary.LittleEndian.Uint16(data[34:]) != 16 ||
		binary.LittleEndian.Uint32(data[40:]) != uint32(len(data)-44) ||
		binary.LittleEndian.Uint32(data[4:]) != uint32(len(data)-8) ||
		(len(data)-44)%audio.SampleBytes != 0 {
		return nil, errors.New("este motor Cloud precisa de WAV PCM mono, 16 kHz, com até 300 segundos")
	}
	duration := float64(len(data)-44) / (audio.SampleRate * audio.SampleBytes)
	if chunk.Duration <= 0 || math.IsNaN(chunk.Duration) || math.IsInf(chunk.Duration, 0) ||
		math.Abs(duration-chunk.Duration) > 0.001 {
		return nil, errors.New("a duração do trecho não corresponde às amostras WAV")
	}
	return data[44:], nil
}

func speechTimeout(duration float64) time.Duration {
	return time.Duration(max(90, min(360, duration+45)) * float64(time.Second))
}

func transcribeError(err error, profile string) error {
	if err == nil {
		return nil
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "AccessDenied", "AccessDeniedException", "UnauthorizedException":
			return &Error{Cause: err, Message: "O perfil AWS precisa de permissão para Amazon Transcribe; " +
				"para arquivos, confira também o acesso ao bucket S3 configurado."}
		case "BadRequestException", "ValidationException":
			return &Error{Cause: err, Message: "O Amazon Transcribe não aceitou o pedido. " +
				"Confira o idioma, a região e, para arquivos, o bucket S3 na mesma região."}
		case "NoSuchBucket", "PermanentRedirect", "IllegalLocationConstraintException":
			return &Error{Cause: err, Message: "O bucket S3 não foi encontrado nesta região. " +
				"Confira seu nome e use a mesma região do Amazon Transcribe."}
		case "LimitExceededException", "ThrottlingException":
			return &Error{Cause: err, Message: "O Amazon Transcribe limitou as chamadas. " +
				"Aguarde e tente novamente; o áudio está preservado."}
		}
	}
	return readableError(err, profile)
}

func incompleteSpeech(engine string) error {
	return &Error{Cause: ErrIncompleteResponse,
		Message: fmt.Sprintf("%s não concluiu a transcrição deste trecho. Tente novamente.", engine)}
}

func waitSpeech(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
