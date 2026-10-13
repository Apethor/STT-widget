package desktop

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/recorder"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2/test"
)

func TestRecognitionAfterRepeatedProcessingOnlyTranscribesAndNeverStartsLLM(t *testing.T) {
	var processingCalls, recognitionCalls atomic.Int32
	r := new(fakeRecording)
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (transcript.Result, error) {
		index := recognitionCalls.Add(1)
		return resultFor(source, fmt.Sprintf("Pergunta ditada %d: qual é o próximo passo?", index)), nil
	}), func(deps *dependencies) {
		deps.newRecorder = func(int, string) recording { return r }
		deps.process = func(_ context.Context, _ config.Settings, request processing.Request) (processing.Result, error) {
			processingCalls.Add(1)
			return processing.Result{Text: "Resposta por clique: " + request.Text}, nil
		}
	})
	doc := h.d.document
	doc.editor.SetText("Pedido inicial")
	for index := 1; index <= 4; index++ {
		if index == 1 {
			test.Tap(doc.processButton)
		} else {
			test.Tap(doc.processFollowButton)
		}
		h.await(func() bool { return !doc.processBusy })
		output := doc.processEditor.Text
		source := historyAudio(t, h.d.directory, fmt.Sprintf("20261009-13000%d.wav", index))
		r.update(func(state *recorder.Snapshot) { state.Path, state.Duration = source, 6 })
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureRunning })
		test.Tap(h.d.recordButton)
		h.await(func() bool { return h.d.capture == captureIdle && !doc.busy })
		if processingCalls.Load() != int32(index) || recognitionCalls.Load() != int32(index) ||
			!strings.Contains(doc.editor.Text, fmt.Sprintf("Pergunta ditada %d:", index)) || doc.processEditor.Text != output {
			t.Fatal("reconhecer respondeu ao pedido ditado ou iniciou Processar sem clique")
		}
	}
}
