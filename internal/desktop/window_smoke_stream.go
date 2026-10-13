//go:build windows && windowsmoke

package desktop

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"
)

type windowSmokeInput struct {
	mu      sync.Mutex
	receive func([]byte, error)
	rate    int
}

func (in *windowSmokeInput) Open(ctx context.Context, _, rate int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.rate = rate
	return ctx.Err()
}
func (in *windowSmokeInput) Start(ctx context.Context, receive func([]byte, error)) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.receive = receive
	return ctx.Err()
}
func (in *windowSmokeInput) Close() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.receive = nil
	return nil
}
func (in *windowSmokeInput) feed(seconds float64) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.receive == nil || seconds <= 0 || seconds > 10 {
		return
	}
	frames := int(seconds * float64(in.rate))
	buffer := make([]byte, 32000)
	for frames > 0 {
		count := min(frames, len(buffer)/2)
		for index := range count {
			binary.LittleEndian.PutUint16(buffer[index*2:], uint16(index%2000))
		}
		in.receive(buffer[:count*2], nil)
		frames -= count
	}
}

func windowSmokeStreamResult(ctx context.Context, source string, onPartial func(transcript.Partial)) (string, bool, error) {
	name := filepath.Base(source)
	if !strings.HasPrefix(name, "microphone-") {
		return "", false, nil
	}
	if strings.Contains(name, "-000000000000-") {
		if onPartial != nil {
			onPartial(transcript.Partial{Text: "Fala sintética antes da screenshot."})
		}
		if err := waitContext(ctx, 4*time.Second); err != nil {
			return "", true, err
		}
		return "Fala sintética antes da screenshot.", true, nil
	}
	return "Fala sintética depois da screenshot.", true, nil
}

func windowSmokeProcess(directory string) func(context.Context, config.Settings, processing.Request) (processing.Result, error) {
	return func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
		snapshot := struct {
			Text    string            `json:"text"`
			History []processing.Turn `json:"history"`
		}{Text: request.Text, History: request.History}
		if err := store.WriteJSON(filepath.Join(directory, "process-request.json"), snapshot); err != nil {
			return processing.Result{}, err
		}
		return processing.Result{Text: "Resposta sintética: " + request.Text}, nil
	}
}
