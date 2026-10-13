package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/recorder"
	"ai-talker/internal/store"
	"ai-talker/internal/transcript"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type runnerFunc func(context.Context, string, config.Settings, transcript.Request) (transcript.Result, error)

func (f runnerFunc) Transcribe(
	ctx context.Context,
	source string,
	settings config.Settings,
	request transcript.Request,
) (transcript.Result, error) {
	return f(ctx, source, settings, request)
}

type testHarness struct {
	t      *testing.T
	d      *desktop
	app    fyne.App
	queue  chan func()
	cancel context.CancelFunc
}

// Fyne's test driver runs Do immediately on the caller's goroutine. This queue
// supplies the same serialization as the real UI driver, so -race checks our
// workers against actual editor actions rather than a concurrent test driver.
func newHarness(t *testing.T, service Runner, configure func(*dependencies)) *testHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	queue := make(chan func(), 256)
	deps := dependencies{
		dispatch: func(callback func()) {
			select {
			case queue <- callback:
			case <-ctx.Done():
			}
		},
		newRecorder:  func(int, string) recording { return &fakeRecording{} },
		copy:         func(string) error { return nil },
		openFolder:   func(string) error { return nil },
		setPinned:    func(bool) error { return nil },
		activate:     func() error { return nil },
		minimize:     func() error { return nil },
		darkTitlebar: func(bool) error { return nil },
		microphones:  func() ([]recorder.Device, error) { return []recorder.Device{}, nil },
		profiles:     func() ([]string, error) { return []string{}, nil },
		confirmClose: func(_ fyne.Window, callback func(bool)) { callback(true) },
		now:          time.Now,
	}
	if configure != nil {
		configure(&deps)
	}
	if service == nil {
		service = runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
			transcript.Result, error,
		) {
			return resultFor(source, "Texto desta gravação."), nil
		})
	}
	application := test.NewApp()
	d := newDesktop(ctx, application, config.Default(), t.TempDir(), service, deps)
	d.window.Show()
	h := &testHarness{t: t, d: d, app: application, queue: queue, cancel: cancel}
	t.Cleanup(func() {
		d.stopped.Store(true)
		d.closing = true
		d.cancel()
		cancel()
		d.workers.Wait()
		h.drain()
		for _, window := range append([]fyne.Window{}, application.Driver().AllWindows()...) {
			window.SetOnClosed(nil)
			window.SetCloseIntercept(nil)
			window.Close()
		}
		application.Quit()
	})
	return h
}

func (h *testHarness) drain() {
	h.t.Helper()
	for {
		select {
		case callback := <-h.queue:
			callback()
		default:
			return
		}
	}
}

func (h *testHarness) await(predicate func() bool) {
	h.t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !predicate() {
		select {
		case callback := <-h.queue:
			callback()
		case <-deadline.C:
			h.t.Fatal("a interface não chegou ao estado esperado")
		}
	}
}

func (h *testHarness) settle() {
	h.t.Helper()
	h.d.workers.Wait()
	h.drain()
}

func waitValue[T any](t *testing.T, source <-chan T) T {
	t.Helper()
	select {
	case value := <-source:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("a tarefa não respondeu")
		var zero T
		return zero
	}
}

func resultFor(source, text string) transcript.Result {
	return transcript.Result{
		Text: text, Source: source, DurationSeconds: 125, ElapsedSeconds: 12,
		ModelID: "test-model", Region: "us-east-1", JobID: "desktop-test",
		Parts: []transcript.Part{{Index: 0, Text: text}},
	}
}

func readResult(t *testing.T, path string) transcript.Result {
	t.Helper()
	var result transcript.Result
	if err := store.ReadJSON(path, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func requireFile(t *testing.T, path, content string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != content {
		t.Fatalf("%s contém %q; esperado %q", filepath.Base(path), payload, content)
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s deveria estar ausente: %v", filepath.Base(path), err)
	}
}

func TestDesktopFitsTranscriptionAndProcessingPanels(t *testing.T) {
	h := newHarness(t, nil, nil)
	want := fyne.NewSize(480, 900)
	if size := h.d.window.Canvas().Size(); size != want {
		t.Fatalf("a janela deveria abrir em %v, abriu em %v", want, size)
	}
	minimum := h.d.window.Content().MinSize()
	if minimum.Width > want.Width || minimum.Height > want.Height {
		t.Fatalf("os controles não cabem na janela inicial: mínimo %v", minimum)
	}
	if minimum.Width < 320 || minimum.Height < 700 {
		t.Fatalf("a janela permite reduzir os controles além do mínimo: %v", minimum)
	}
	h.d.editor.SetText("Texto da transcrição para editar.")
	h.d.processEditor.SetText("Mensagem pronta para copiar.")
	h.d.errorLabel.SetText("AWS: não foi possível localizar o perfil de verificação. Confira o perfil nas configurações.")
	h.d.errorLabel.Show()
	h.d.window.Resize(minimum)
	for name, editor := range map[string]*widget.Entry{
		"transcrição": h.d.editor,
		"resultado":   h.d.processEditor,
	} {
		if height := editor.Size().Height; height < editor.MinSize().Height {
			t.Fatalf("o editor de %s ficou ilegível no tamanho mínimo: altura %v", name, height)
		}
	}
}

func TestDesktopLoadsPythonSnapshotWithoutRewritingIt(t *testing.T) {
	h := newHarness(t, nil, nil)
	payload := `{
		"text":"Minha transcrição anterior.", "source":"primeiro.wav",
		"duration_seconds":125, "elapsed_seconds":12,
		"model_id":"test-model", "region":"us-east-1", "job_id":"python",
		"parts":[{"index":0,"text":"Minha transcrição anterior.","speech_seconds":null}],
		"exports":null, "reused_parts":0, "needs_review":true
	}`
	if err := store.Write(h.d.latestPath(), []byte(payload)); err != nil {
		t.Fatal(err)
	}
	h.d.loadPrevious()
	if h.d.editor.Text != "Minha transcrição anterior." || h.d.transcriptName.Text != "Transcrição anterior" {
		t.Fatalf("a transcrição anterior não foi carregada: %q", h.d.editor.Text)
	}
	if h.d.copyButton.Disabled() || h.d.saveButton.Disabled() || h.d.clearButton.Disabled() {
		t.Fatal("os controles do editor deveriam estar disponíveis")
	}
	if !h.d.lastResult.NeedsReview || h.d.lastResult.Parts[0].SpeechSeconds != 0 {
		t.Fatal("os campos Python opcionais não foram preservados")
	}
	if !h.d.retryButton.Visible() || h.d.retryButton.Disabled() {
		t.Fatal("o snapshot marcado para revisão deve permitir tentar novamente")
	}
	requireFile(t, h.d.latestPath(), payload)
}

func TestDesktopClearAndCopyDuringWaitRejectLateResults(t *testing.T) {
	type call struct {
		ctx     context.Context
		source  string
		request transcript.Request
	}
	calls := make(chan call, 1)
	release := make(chan struct{})
	releaseService := sync.OnceFunc(func() { close(release) })
	copied := make(chan string, 1)
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, request transcript.Request) (
		transcript.Result, error,
	) {
		calls <- call{ctx: ctx, source: source, request: request}
		<-release // Simulates an AWS call that finishes after foreground cancel.
		return resultFor(source, "Resultado atrasado do áudio antigo."), nil
	}), func(deps *dependencies) {
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	t.Cleanup(releaseService)
	original := filepath.Join(h.d.directory, "primeiro.wav")
	if err := os.WriteFile(original, []byte("áudio preservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := resultFor(original, "Transcrição do primeiro áudio.")
	if !h.d.showResult(previous, true) {
		t.Fatal("não foi possível preparar a transcrição anterior")
	}
	h.d.startJob("segundo.wav")
	current := waitValue(t, calls)
	if current.request.PersistLatest {
		t.Fatal("a tarefa deve deixar a persistência da última transcrição com a UI")
	}
	if current.request.CacheDir != "" {
		t.Fatal("o desktop deve usar o cache Go definido pelo serviço e preservar o cache Python")
	}
	requireAbsent(t, h.d.latestPath())
	if h.d.editor.Text != "" || h.d.recordButton.Text != "Cancelar transcrição" || h.d.clearButton.Disabled() {
		t.Fatal("a tarefa não liberou o editor nem removeu o texto anterior")
	}
	test.Type(h.d.editor, "Texto atual com TypeScript e português.")
	test.Tap(h.d.copyButton)
	if text := waitValue(t, copied); text != h.d.editor.Text {
		t.Fatalf("foi copiado um texto antigo: %q", text)
	}
	test.Tap(h.d.clearButton)
	if h.d.busy || current.ctx.Err() == nil || h.d.ctx.Err() != nil {
		t.Fatal("limpar deve cancelar somente a tarefa e liberar os controles imediatamente")
	}
	if !h.d.copyButton.Disabled() || !h.d.saveButton.Disabled() || h.d.clearButton.Disabled() {
		t.Fatal("os botões não acompanham o conteúdo vazio do editor")
	}
	current.request.OnProgress(transcript.Progress{Message: "Progresso antigo", Fraction: 0.9})
	releaseService()
	h.settle()
	if h.d.editor.Text != "" || h.d.progress.Value != 0 || h.d.errorLabel.Visible() {
		t.Fatal("um evento antigo recolocou conteúdo na interface")
	}
	requireAbsent(t, h.d.latestPath())
	requireFile(t, original, "áudio preservado")
	drafts, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil || len(drafts) != 1 {
		t.Fatalf("a edição deveria ter um rascunho: %v, %v", drafts, err)
	}
	requireFile(t, drafts[0], "Texto atual com TypeScript e português.\n")
}

func TestDesktopCancelButtonKeepsAppContextAndControlsUsable(t *testing.T) {
	calls := make(chan context.Context, 1)
	h := newHarness(t, runnerFunc(func(ctx context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		calls <- ctx
		<-ctx.Done()
		return transcript.Result{}, ctx.Err()
	}), nil)
	h.d.startJob("audio.wav")
	job := waitValue(t, calls)
	test.Tap(h.d.recordButton)
	if job.Err() == nil || h.d.ctx.Err() != nil || h.d.busy {
		t.Fatal("cancelar deve liberar a tarefa sem cancelar a aplicação")
	}
	if h.d.recordButton.Disabled() || h.d.newButton.Disabled() || h.d.clearButton.Disabled() {
		t.Fatal("cancelar deixou controles indisponíveis")
	}
	h.settle()
	if h.d.recordState.Text != "Transcrição cancelada." || h.d.progress.Value != 0 {
		t.Fatal("o evento de cancelamento antigo mudou o estado atual")
	}
}

func TestDesktopSilentResultReplacesPreviousSnapshot(t *testing.T) {
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return transcript.Result{
			Source: source, DurationSeconds: 2, Parts: []transcript.Part{{Index: 0, NoSpeech: true}},
		}, nil
	}), nil)
	if !h.d.showResult(resultFor("primeiro.wav", "Texto do primeiro áudio."), true) {
		t.Fatal("preparar o resultado anterior")
	}
	h.d.startJob("silencio.wav")
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "" || h.d.recordState.Text != "Nenhuma fala detectada." {
		t.Fatal("o silêncio não substituiu a primeira transcrição")
	}
	if !h.d.copyButton.Disabled() || h.d.clearButton.Disabled() || h.d.retryButton.Visible() {
		t.Fatal("os controles não representam o resultado silencioso")
	}
	saved := readResult(t, h.d.latestPath())
	if saved.Source != "silencio.wav" || saved.Text != "" {
		t.Fatalf("o snapshot silencioso foi perdido: %+v", saved)
	}
}

func TestDesktopSpeechWithoutTextOffersRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result transcript.Result
	}{
		{name: "fala detectada", result: transcript.Result{Parts: []transcript.Part{{DetectedSpeech: true}}}},
		{name: "revisão persistida", result: transcript.Result{NeedsReview: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
				transcript.Result, error,
			) {
				result := tc.result
				result.Source = source
				return result, nil
			}), nil)
			h.d.startJob("fala.wav")
			h.await(func() bool { return !h.d.busy })
			if h.d.recordState.Text != "Nenhuma fala reconhecida." || !h.d.retryButton.Visible() {
				t.Fatal("fala sem texto não foi distinguida de silêncio")
			}
			if h.d.retryButton.Disabled() || h.d.editor.Text != "" {
				t.Fatal("o usuário deve poder tentar novamente sem texto antigo")
			}
		})
	}
}

func TestDesktopErrorIsNonmodalAndKeepsPartialEditable(t *testing.T) {
	h := newHarness(t, runnerFunc(func(_ context.Context, _ string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		return transcript.Result{}, fmt.Errorf("camada externa: %w", &transcript.Error{
			Cause: errors.New("AWS falhou"), PartialText: "Parte já reconhecida.",
		})
	}), nil)
	h.d.startJob("novo.wav")
	test.Type(h.d.editor, "Edição feita enquanto espero.")
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "Parte já reconhecida." || h.d.transcriptName.Text != "Transcrição parcial" {
		t.Fatal("o erro não apresentou a transcrição parcial")
	}
	if !h.d.errorLabel.Visible() || !strings.Contains(h.d.errorLabel.Text, "AWS falhou") {
		t.Fatal("o erro deveria aparecer na própria interface")
	}
	if len(h.d.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("o erro não deve abrir um diálogo modal")
	}
	if h.d.clearButton.Disabled() || h.d.copyButton.Disabled() || h.d.newButton.Disabled() {
		t.Fatal("o erro bloqueou os controles")
	}
	requireAbsent(t, h.d.latestPath())
	test.Tap(h.d.clearButton)
	if h.d.errorLabel.Visible() || h.d.editor.Text != "" {
		t.Fatal("limpar não recuperou a interface após o erro")
	}
}

func TestDesktopReviewedTextStaysEditableAndOffersRetry(t *testing.T) {
	h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, _ transcript.Request) (
		transcript.Result, error,
	) {
		result := resultFor(source, "Texto que precisa de conferência.")
		result.NeedsReview = true
		return result, nil
	}), nil)
	h.d.startJob("conferir.wav")
	h.await(func() bool { return !h.d.busy })
	if !h.d.errorLabel.Visible() || !h.d.retryButton.Visible() || h.d.retryButton.Disabled() {
		t.Fatal("a resposta suspeita não ofereceu conferência e retomada")
	}
	if h.d.editor.Text != "Texto que precisa de conferência." || h.d.copyButton.Disabled() {
		t.Fatal("o texto para conferência deixou de ser editável e copiável")
	}
	if saved := readResult(t, h.d.latestPath()); !saved.NeedsReview {
		t.Fatal("o aviso de conferência foi perdido ao salvar o resultado")
	}
}

func TestDesktopStaleProgressAndErrorsCannotChangeNewResult(t *testing.T) {
	for _, tail := range []string{"resultado", "erro parcial", "cancelamento"} {
		t.Run(tail, func(t *testing.T) {
			oldCall := make(chan transcript.Request, 1)
			release := make(chan struct{})
			releaseService := sync.OnceFunc(func() { close(release) })
			h := newHarness(t, runnerFunc(func(_ context.Context, source string, _ config.Settings, request transcript.Request) (
				transcript.Result, error,
			) {
				if source == "antigo.wav" {
					oldCall <- request
					<-release
					switch tail {
					case "erro parcial":
						return transcript.Result{}, &transcript.Error{
							Cause: errors.New("erro antigo"), PartialText: "Texto antigo.",
						}
					case "cancelamento":
						return transcript.Result{}, context.Canceled
					default:
						return resultFor(source, "Texto antigo."), nil
					}
				}
				return resultFor(source, "Texto do segundo áudio."), nil
			}), nil)
			t.Cleanup(releaseService)
			h.d.startJob("antigo.wav")
			request := waitValue(t, oldCall)
			h.d.startJob("novo.wav")
			h.await(func() bool { return !h.d.busy })
			before := h.d.status.Text
			request.OnProgress(transcript.Progress{Message: "Progresso antigo", Fraction: 0.1})
			releaseService()
			h.settle()
			if h.d.editor.Text != "Texto do segundo áudio." || h.d.errorLabel.Visible() || h.d.status.Text != before {
				t.Fatal("o evento antigo alterou a nova geração")
			}
			if saved := readResult(t, h.d.latestPath()); saved.Source != "novo.wav" {
				t.Fatal("o evento antigo sobrescreveu a última transcrição")
			}
		})
	}
}

func TestDesktopClearAndNewAudioPreserveFilesAndUniqueDrafts(t *testing.T) {
	h := newHarness(t, nil, nil)
	source := filepath.Join(h.d.directory, "gravação.wav")
	export := filepath.Join(h.d.directory, "transcricao.txt")
	for path, content := range map[string]string{source: "áudio original", export: "texto exportado"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result := resultFor(source, "Texto original.")
	result.Exports = map[string]string{"txt": export}
	if !h.d.showResult(result, true) {
		t.Fatal("preparar o resultado")
	}
	h.d.editor.SetText("Texto original.\nMinha edição.")
	test.Tap(h.d.clearButton)
	h.d.editor.SetText("Outra edição.")
	test.Tap(h.d.newButton)
	if h.d.lastAudio != "" || h.d.lastResult != nil || h.d.timer.Text != "00:00" || h.d.progress.Value != 0 {
		t.Fatal("novo áudio não reiniciou o estado da interface")
	}
	requireAbsent(t, h.d.latestPath())
	requireFile(t, source, "áudio original")
	requireFile(t, export, "texto exportado")
	drafts, err := filepath.Glob(filepath.Join(h.d.directory, "rascunhos", "*.txt"))
	if err != nil || len(drafts) != 2 {
		t.Fatalf("as duas edições precisam de rascunhos distintos: %v, %v", drafts, err)
	}
	for _, path := range drafts {
		payload, err := os.ReadFile(path)
		if err != nil || len(payload) == 0 {
			t.Fatalf("um rascunho está vazio ou ilegível: %s, %v", path, err)
		}
	}
	h.d.loadPrevious()
	if h.d.editor.Text != "" {
		t.Fatal("o texto limpo reapareceu ao recarregar")
	}
}

func TestDesktopDraftFailurePreservesCurrentEditorAndStopsReplacement(t *testing.T) {
	h := newHarness(t, nil, nil)
	if err := os.WriteFile(filepath.Join(h.d.directory, "rascunhos"), []byte("arquivo reservado"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.editor.SetText("Minha edição ainda não salva.")
	h.d.startJob("novo.wav")
	if h.d.busy || h.d.editor.Text != "Minha edição ainda não salva." || !h.d.errorLabel.Visible() {
		t.Fatal("a falha em preservar o rascunho descartou a edição")
	}
	if h.d.copyButton.Disabled() || h.d.clearButton.Disabled() || h.d.recordButton.Disabled() {
		t.Fatal("a falha do rascunho bloqueou o editor")
	}
}

func TestDesktopInvalidPreviousJSONIsPreservedAndRecoverable(t *testing.T) {
	h := newHarness(t, nil, nil)
	if err := store.Write(h.d.latestPath(), []byte("{incompleto")); err != nil {
		t.Fatal(err)
	}
	h.d.loadPrevious()
	requireFile(t, h.d.latestPath(), "{incompleto")
	if !h.d.errorLabel.Visible() || h.d.clearButton.Disabled() {
		t.Fatal("o erro de leitura deve permitir a recuperação")
	}
	h.d.startJob("novo.wav")
	h.await(func() bool { return !h.d.busy })
	if h.d.editor.Text != "Texto desta gravação." || h.d.errorLabel.Visible() {
		t.Fatal("a aplicação não se recuperou com outro áudio")
	}
}

func TestDesktopRepeatedEditingActionsUseActualContent(t *testing.T) {
	copied := make(chan string, 1)
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.copy = func(text string) error { copied <- text; return nil }
	})
	for index := range 20 {
		h.d.showResult(resultFor("audio.wav", fmt.Sprintf("Texto original %d.", index)), false)
		h.d.editor.SetText(fmt.Sprintf("Edição %d: Python e AWS.", index))
		test.Tap(h.d.copyButton)
		if got := waitValue(t, copied); got != h.d.editor.Text {
			t.Fatalf("ciclo %d copiou %q", index, got)
		}
		h.settle()
		test.Tap(h.d.clearButton)
		test.Type(h.d.editor, fmt.Sprintf("Novo texto %d.", index))
		if h.d.copyButton.Disabled() {
			t.Fatalf("ciclo %d deixou copiar indisponível após a edição", index)
		}
		test.Tap(h.d.newButton)
		if h.d.editor.Text != "" || h.d.clearButton.Disabled() {
			t.Fatalf("ciclo %d não terminou com o editor limpo", index)
		}
	}
}

func TestClockText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds float64
		want    string
	}{
		{name: "zero", seconds: 0, want: "00:00"},
		{name: "negativo", seconds: -2, want: "00:00"},
		{name: "fração", seconds: 125.9, want: "02:05"},
		{name: "hora", seconds: 3661, want: "01:01:01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clockText(tc.seconds); got != tc.want {
				t.Fatalf("clockText(%v) = %q, esperado %q", tc.seconds, got, tc.want)
			}
		})
	}
}
