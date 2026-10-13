package transcript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/store"
)

const cacheVersion = 3

type Preparer interface {
	Duration(context.Context, string) (float64, error)
	Split(context.Context, string, audio.ChunkOptions, func(audio.Chunk) error) error
}

type livePreparer interface {
	PrepareLive(context.Context, string) (audio.Chunk, error)
}

type liveContextPreparer interface {
	PrepareLiveWithContext(context.Context, string, uint64) (audio.Chunk, error)
}

type Options struct {
	DataDir          string
	Audio            Preparer
	CloudFactory     Factory
	LocalFactory     Factory
	APIFactory       Factory
	LocalFingerprint func(context.Context, config.Settings) (string, error)
	RequestTimeout   time.Duration
	MaxRequests      int
}

type Request struct {
	// NativeContextSamples counts preceding audio after decoding to mono
	// 16 kHz, for both the native shortcut and the general codec fallback.
	NativeContextSamples uint64
	OutputDir            string
	CacheDir             string
	Workers              int
	OnProgress           func(Progress)
	// OnPartial runs on a request worker and can be called concurrently for
	// different parts. The caller must marshal UI updates to its own loop.
	OnPartial func(Partial)
	// NativePCM selects the bounded WAV shortcut for clips exported by the
	// recorder, if supplied by the preparer. It never changes imported audio.
	NativePCM bool
	Streaming bool
	// ContinueOnError collects later accepted parts into an incomplete result.
	// Consumers using the legacy fail-fast flow can still leave it false.
	ContinueOnError bool
	PersistLatest   bool
}

type Service struct {
	options          Options
	automaticTimeout bool
	ctx              context.Context
	cancel           context.CancelFunc
	slots            *concurrencyGate
	localSlot        *concurrencyGate
	jobs             sync.WaitGroup
	mu               sync.Mutex
	closing          bool
	streamCloud      map[string]*sharedStreamCloudEngine
}

func New(ctx context.Context, options Options) (*Service, error) {
	if options.Audio == nil || options.CloudFactory == nil || options.DataDir == "" {
		return nil, errors.New("informe áudio, transcritor e diretório de dados")
	}
	if options.MaxRequests == 0 {
		options.MaxRequests = 4
	}
	if options.MaxRequests < 1 || options.MaxRequests > 4 {
		return nil, errors.New("use entre uma e quatro chamadas simultâneas")
	}
	automaticTimeout := options.RequestTimeout == 0
	if automaticTimeout {
		options.RequestTimeout = 150 * time.Second
	}
	if options.RequestTimeout <= 0 {
		return nil, errors.New("o prazo das chamadas deve ser positivo")
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Service{
		options: options, automaticTimeout: automaticTimeout, ctx: lifetime, cancel: cancel,
		slots:       newConcurrencyGate(options.MaxRequests),
		localSlot:   newConcurrencyGate(1),
		streamCloud: make(map[string]*sharedStreamCloudEngine),
	}, nil
}

// Close ends the application lifetime, cancelling all outstanding requests and
// waiting for each job to release its transcriber and file lock.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closing = true
	s.cancel()
	s.mu.Unlock()
	s.jobs.Wait()
	s.mu.Lock()
	engines := make([]Transcriber, 0, len(s.streamCloud))
	for _, entry := range s.streamCloud {
		<-entry.ready
		if entry.engine != nil {
			engines = append(engines, entry.engine)
		}
	}
	s.streamCloud = nil
	s.mu.Unlock()
	var failures []error
	for _, engine := range engines {
		if err := engine.Close(); err != nil {
			failures = append(failures, fmt.Errorf("fechar transcritor Cloud compartilhado: %w", err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return errors.New("o serviço de transcrição foi encerrado")
	}
	s.jobs.Add(1)
	return nil
}

func (s *Service) Transcribe(
	ctx context.Context,
	source string,
	settings config.Settings,
	request Request,
) (result Result, err error) {
	if settings.SupportsStructuredDiarization() {
		settings.SpeakerLabels = true
	}
	if err := settings.Validate(); err != nil {
		return result, err
	}
	if request.Workers == 0 {
		request.Workers = 2
	}
	if request.Workers < 1 || request.Workers > 4 {
		return result, errors.New("use entre uma e quatro chamadas simultâneas")
	}
	if settings.TranscriptionProvider() == config.ProviderWhisper {
		// Loading several local models at once can exhaust memory. Local parts
		// share one slot and inherit the foreground job's cancellation.
		request.Workers = 1
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.begin(); err != nil {
		return result, err
	}
	jobCtx, cancel := context.WithCancel(ctx)
	stopLifetime := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stopLifetime()
	job := &job{
		service: s, source: source, settings: settings, request: request, started: time.Now(),
		parts: make(map[int]Part), failures: make(map[int]error), completed: make(chan completion, request.Workers),
		retryUpdates: make(chan string, request.Workers),
	}
	defer job.finish()
	result, err = job.run(jobCtx)
	if err != nil && jobCtx.Err() != nil {
		return result, jobCtx.Err()
	}
	if err != nil {
		partial := Merge(orderedParts(job.parts), settings.OverlapSeconds > 0)
		if partial != "" && job.directory != "" {
			if saveErr := store.Write(filepath.Join(job.directory, "partial.txt"), []byte(partial+"\n")); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
		}
		return result, &Error{Cause: err, PartialText: partial, JobID: job.id, Result: result}
	}
	return result, err
}

type completion struct {
	part Part
	err  error
}

type job struct {
	service            *Service
	source             string
	settings           config.Settings
	request            Request
	directory          string
	id                 string
	lock               *platform.FileLock
	cloud              Transcriber
	cloudSetupSeconds  float64
	cloudSetupReported bool
	local              Transcriber
	localMu            sync.Mutex
	started            time.Time
	parts              map[int]Part
	failures           map[int]error
	completed          chan completion
	retryUpdates       chan string
	ctx                context.Context
	calls              sync.WaitGroup
	pending            int
	count              int
	reused             int
	estimated          int
	coveredEnd         float64
}

func (j *job) finish() {
	cleanup := func() {
		j.calls.Wait()
		if j.cloud != nil {
			if err := j.cloud.Close(); err != nil {
				slog.Warn("falha ao fechar o transcritor", "error", err)
			}
		}
		if j.local != nil {
			if err := j.local.Close(); err != nil {
				slog.Warn("falha ao fechar o motor de recuperação", "error", err)
			}
		}
		if j.lock != nil {
			if err := j.lock.Close(); err != nil {
				slog.Warn("falha ao liberar o cache", "error", err)
			}
		}
		j.service.jobs.Done()
	}
	if j.pending > 0 {
		// Already sent requests have finite deadlines owned by the service. The
		// job lock stays held until their successful parts reach the cache.
		go cleanup()
		return
	}
	cleanup()
}

func (j *job) progress(stage, message string, fraction float64) {
	if j.request.OnProgress != nil {
		completed := 0
		for _, part := range j.parts {
			if part.Error == "" && !part.NeedsReview {
				completed++
			}
		}
		j.request.OnProgress(Progress{
			Stage: stage, Message: message, Fraction: fraction,
			Completed: completed, Total: max(j.estimated, j.count),
		})
	}
}

func (j *job) run(ctx context.Context) (Result, error) {
	j.ctx = ctx
	var empty Result
	source, err := filepath.Abs(j.source)
	if err != nil {
		return empty, fmt.Errorf("localizar o áudio: %w", err)
	}
	j.source = source
	details, err := os.Stat(source)
	if err != nil || details.IsDir() {
		return empty, errors.New("o arquivo de áudio não foi encontrado")
	}
	if !supported(source) {
		return empty, errors.New("formato não reconhecido; use M4A, MP3, WAV, OGG, FLAC, AAC ou WebM")
	}
	j.progress("prepare", "Preparando o áudio…", 0.02)
	var liveChunk *audio.Chunk
	preparationVersion := 0
	if preparer, ok := j.service.options.Audio.(livePreparer); ok && j.request.NativePCM {
		var prepared audio.Chunk
		var err error
		if j.request.NativeContextSamples > 0 {
			contextPreparer, ok := j.service.options.Audio.(liveContextPreparer)
			if !ok {
				return empty, errors.New("o preparador de áudio não suporta contexto no Stream")
			}
			prepared, err = contextPreparer.PrepareLiveWithContext(ctx, source, j.request.NativeContextSamples)
		} else {
			prepared, err = preparer.PrepareLive(ctx, source)
		}
		if err != nil {
			return empty, err
		}
		liveChunk = &prepared
		preparationVersion = audio.StreamPreparationVersion
	}
	hash, err := sourceHash(ctx, source)
	if err != nil {
		return empty, err
	}
	modelFingerprint := ""
	if j.settings.TranscriptionProvider() == config.ProviderWhisper && j.service.options.LocalFingerprint != nil {
		modelFingerprint, err = j.service.options.LocalFingerprint(ctx, j.settings)
		if err != nil {
			return empty, err
		}
	} else if j.fallbackEnabled() && j.service.options.LocalFingerprint != nil {
		// An unavailable local engine must not prevent the Cloud request.
		// Include its identity when present, so recovered text never crosses
		// a change of model, file or inference settings.
		modelFingerprint, err = j.service.options.LocalFingerprint(ctx, j.settings)
		if err != nil {
			if ctx.Err() != nil {
				return empty, ctx.Err()
			}
			modelFingerprint = "unavailable"
		}
	}
	streamPrompt := 0
	if j.request.OnPartial != nil && j.settings.TranscriptionProvider() == config.ProviderBedrock {
		streamPrompt = config.TranscriptionStreamPromptVersion
	}
	j.id, err = fingerprintWithPreparation(hash, j.settings, modelFingerprint, preparationVersion, streamPrompt, j.request.NativeContextSamples)
	if err != nil {
		return empty, err
	}
	cache := j.request.CacheDir
	if cache == "" {
		cache = filepath.Join(j.service.options.DataDir, "jobs-go-v1")
	}
	j.directory = filepath.Join(cache, j.id)
	if err := os.MkdirAll(j.directory, 0o700); err != nil {
		return empty, fmt.Errorf("preparar o cache: %w", err)
	}
	if err := j.acquireCache(ctx); err != nil {
		return empty, err
	}
	if err := j.prepare(ctx, source, liveChunk); err != nil {
		return empty, err
	}
	for j.pending > 0 {
		if err := j.collect(ctx); err != nil {
			return empty, err
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if j.count == 0 {
		return empty, errors.New("o áudio está vazio")
	}
	if len(j.parts) != j.count {
		return empty, errors.New("uma parte do áudio não foi concluída")
	}
	ordered := orderedParts(j.parts)
	var actualDuration float64
	var needsReview bool
	for _, part := range ordered {
		actualDuration = max(actualDuration, part.Start+part.Duration)
		needsReview = needsReview || part.NeedsReview || (part.DetectedSpeech && strings.TrimSpace(part.Text) == "")
	}
	text := Merge(ordered, j.settings.OverlapSeconds > 0)
	if j.settings.SpeakerLabels {
		if diarized, ok := DiarizedText(ordered, captureSpeakerPrefix(j.settings.Capture.SourceMode())); ok {
			text = diarized
		}
	}
	result := Result{
		Text: text, Source: source,
		DurationSeconds: actualDuration, ElapsedSeconds: time.Since(j.started).Seconds(),
		ModelID: j.settings.ModelID(), Region: j.settings.Region, Parts: ordered,
		ReusedParts: j.reused, JobID: j.id, NeedsReview: needsReview,
	}
	if j.settings.TranscriptionProvider() == config.ProviderWhisper {
		result.Region = "local"
	} else if j.settings.TranscriptionProvider() == config.ProviderAPI {
		result.Region = "api"
	}
	if len(j.failures) > 0 {
		failures := make([]error, 0, len(j.failures))
		for _, part := range ordered {
			if failure := j.failures[part.Index]; failure != nil {
				failures = append(failures, fmt.Errorf("parte %d: %w", part.Index+1, failure))
			}
		}
		result.NeedsReview = true
		if err := store.WriteJSON(filepath.Join(j.directory, "partial-result.json"), result); err != nil {
			failures = append(failures, err)
		}
		return result, errors.Join(failures...)
	}
	if strings.TrimSpace(result.Text) != "" {
		output := j.request.OutputDir
		if output == "" {
			output = filepath.Join(j.service.options.DataDir, "transcricoes")
		}
		if err := SaveExports(&result, output); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := store.WriteJSON(filepath.Join(j.directory, "result.json"), result); err != nil {
		return result, err
	}
	if j.request.PersistLatest {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		if err := store.WriteJSON(filepath.Join(j.service.options.DataDir, "last-transcript.json"), result); err != nil {
			return result, err
		}
	}
	message := "Texto pronto."
	if result.Text == "" {
		message = "Nenhuma fala detectada."
	}
	if needsReview {
		message = "Há partes que precisam de conferência. Compare o texto com o áudio."
	}
	j.progress("done", message, 1)
	return result, nil
}

func (j *job) acquireCache(ctx context.Context) error {
	path := filepath.Join(j.directory, ".lock")
	var timer *time.Timer
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err := platform.TryLock(path)
		if err == nil {
			j.lock = lock
			return ctx.Err()
		}
		if !errors.Is(err, platform.ErrLocked) {
			return fmt.Errorf("abrir o cache: %w", err)
		}
		if timer == nil {
			// The previous owner may still be saving successful in-flight parts.
			// Only its OS lock can authorize reading or updating this cache.
			j.progress("wait", "Aguardando a tentativa anterior concluir e salvar os trechos…", 0)
			timer = time.NewTimer(100 * time.Millisecond)
			defer timer.Stop()
		} else {
			timer.Reset(100 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (j *job) prepare(ctx context.Context, source string, liveChunk *audio.Chunk) error {
	if liveChunk != nil {
		j.estimated = 1
		return j.accept(ctx, *liveChunk)
	}
	duration, err := j.service.options.Audio.Duration(ctx, source)
	if err != nil {
		return err
	}
	j.estimated = max(1, int(math.Ceil(max(0.001, duration-j.settings.OverlapSeconds)/
		(float64(j.settings.ChunkSeconds)-j.settings.OverlapSeconds))))
	options := audio.ChunkOptions{
		ChunkSeconds: j.settings.ChunkSeconds, OverlapSeconds: j.settings.OverlapSeconds,
		KeepPCM:        j.settings.SupportsSubdivision() && !j.request.Streaming,
		ContextSamples: j.request.NativeContextSamples,
		OnDecode: func(seconds float64) {
			if len(j.parts) > 0 || j.pending > 0 {
				return
			}
			fraction := min(0.14, 0.02+0.12*seconds/max(duration, seconds, 1))
			j.progress("prepare", "Preparando o áudio…", fraction)
		},
	}
	if j.settings.UsesPCM() {
		options.Format = audio.FormatWAV
	}
	return j.service.options.Audio.Split(ctx, source, options, func(chunk audio.Chunk) error {
		return j.accept(ctx, chunk)
	})
}

func (j *job) accept(ctx context.Context, chunk audio.Chunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chunk.Index != j.count {
		return errors.New("o decodificador entregou partes fora de ordem")
	}
	end := chunk.Start + chunk.Duration
	validTime := chunk.Start >= 0 && chunk.Duration > 0 && !math.IsNaN(end) && !math.IsInf(end, 0)
	if !validTime {
		return errors.New("o decodificador entregou uma duração de áudio inválida")
	}
	if chunk.Start > j.coveredEnd+1e-9 {
		return errors.New("uma parte do áudio ficou sem cobertura")
	}
	if end <= j.coveredEnd {
		return errors.New("o decodificador entregou uma parte sem áudio novo")
	}
	j.coveredEnd = end
	j.count++
	if !chunk.HasSpeech {
		part := makePart(chunk, ChunkResult{Usage: make(map[string]any)})
		if err := store.WriteJSON(j.partPath(chunk.Index), part); err != nil {
			return err
		}
		j.parts[chunk.Index] = part
		j.report()
		return nil
	}
	var cached Part
	if err := store.ReadJSON(j.partPath(chunk.Index), &cached); err == nil && validCached(cached, chunk) {
		j.parts[chunk.Index] = cached
		j.reused++
		j.report()
		return nil
	}
	if j.cloud == nil {
		factory := j.service.options.CloudFactory
		if j.settings.TranscriptionProvider() == config.ProviderWhisper {
			factory = j.service.options.LocalFactory
			if factory == nil {
				return errors.New("configure o motor Whisper local nas configurações")
			}
		} else if j.settings.TranscriptionProvider() == config.ProviderAPI {
			factory = j.service.options.APIFactory
			if factory == nil {
				return errors.New("configure a API de transcrição nas configurações")
			}
		}
		cloud, setupSeconds, err := j.service.cloudEngine(
			ctx, j.settings, factory,
			j.request.Streaming && j.settings.TranscriptionProvider() == config.ProviderBedrock,
		)
		if err != nil {
			return err
		}
		if cloud == nil {
			return errors.New("o transcritor não foi inicializado")
		}
		j.cloud = cloud
		j.cloudSetupSeconds = setupSeconds
	}
	j.progress("transcribe",
		fmt.Sprintf("Transcrevendo · parte %d de %d", chunk.Index+1, j.estimated),
		min(0.95, 0.15+0.8*float64(len(j.parts))/float64(max(j.estimated, j.count))))
	j.pending++
	j.calls.Add(1)
	setupSeconds := 0.0
	if !j.cloudSetupReported {
		setupSeconds = j.cloudSetupSeconds
		j.cloudSetupReported = true
	}
	go j.transcribe(chunk, setupSeconds)
	if j.pending >= j.request.Workers {
		return j.collect(ctx)
	}
	return nil
}

func (j *job) transcribe(chunk audio.Chunk, setupSeconds float64) {
	defer j.calls.Done()
	response, err := j.recognizePart(chunk)
	if j.settings.TranscriptionProvider() == config.ProviderBedrock {
		if response.Usage == nil {
			response.Usage = make(map[string]any)
		}
		response.Usage["cloud_client_setup_seconds"] = setupSeconds
	}
	if err == nil && j.request.OnPartial != nil && ImplausibleLiveText(response.Text, chunk.Duration) {
		response.Text = ""
		err = errors.New("a resposta do motor continuou além do trecho de áudio; tente reconhecer novamente")
	}
	part := makePart(chunk, response)
	if part.Engine == "" {
		part.Engine = j.settings.ModelID()
	}
	if err == nil {
		err = store.WriteJSON(j.partPath(chunk.Index), part)
	}
	if err != nil {
		part.Error = err.Error()
		part.NeedsReview = true
		part.ReviewReason = part.Error
	}
	// At most Workers results remain after the foreground returns, and the
	// channel has precisely that capacity. Completion never needs the GUI.
	j.completed <- completion{part: part, err: err}
}

func (j *job) requestSlots() *concurrencyGate {
	if j.settings.TranscriptionProvider() == config.ProviderWhisper {
		return j.service.localSlot
	}
	return j.service.slots
}

func (j *job) collect(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-j.retryUpdates:
			j.progress("retry", message,
				min(0.95, 0.15+0.8*float64(len(j.parts))/float64(max(j.estimated, j.count))))
		case completed := <-j.completed:
			j.pending--
			if completed.err != nil {
				if !j.request.ContinueOnError {
					return completed.err
				}
				j.failures[completed.part.Index] = completed.err
			}
			j.parts[completed.part.Index] = completed.part
			j.report()
			return nil
		}
	}
}

func (j *job) report() {
	stage, message := "prepare", "Verificando fala…"
	if j.cloud != nil {
		stage = "transcribe"
		message = fmt.Sprintf("Transcrevendo · %d de %d partes concluídas", len(j.parts), max(j.estimated, j.count))
	}
	j.progress(stage, message, min(0.95, 0.15+0.8*float64(len(j.parts))/float64(max(j.estimated, j.count))))
}

func (j *job) partPath(index int) string {
	return filepath.Join(j.directory, fmt.Sprintf("part-%05d.json", index))
}

func makePart(chunk audio.Chunk, result ChunkResult) Part {
	if result.Usage == nil {
		result.Usage = make(map[string]any)
	}
	text := strings.TrimSpace(result.Text)
	reason := result.ReviewReason
	if chunk.HasSpeech && (text == "" || onlyUnintelligibleMarkers(text)) {
		if reason == "" {
			reason = "Fala detectada sem palavras reconhecidas."
		}
	} else if float64(len(strings.Fields(text))) > max(40, math.Ceil(chunk.Duration*10)) {
		// This generous bound is a warning, not an accuracy score. It catches
		// gross continuations such as hundreds of words for a few seconds.
		reason = "A resposta contém texto demais para a duração do áudio."
	}
	return Part{
		Index: chunk.Index, Start: chunk.Start, Duration: chunk.Duration, Text: text,
		Segments: slices.Clone(result.Segments),
		NoSpeech: text == "", DetectedSpeech: chunk.HasSpeech,
		SpeechSeconds: chunk.SpeechSeconds, Usage: result.Usage, ElapsedSeconds: result.ElapsedSeconds,
		NeedsReview: reason != "", ReviewReason: reason,
		Engine: result.Engine, Children: result.Children, Recovery: result.Recovery,
	}
}

func validCached(part Part, chunk audio.Chunk) bool {
	return !part.NeedsReview && part.Error == "" && !onlyUnintelligibleMarkers(part.Text) &&
		part.Index == chunk.Index && strings.TrimSpace(part.Text) != "" &&
		math.Abs(part.Start-chunk.Start) < 0.001 && math.Abs(part.Duration-chunk.Duration) < 0.001
}

func orderedParts(parts map[int]Part) []Part {
	result := make([]Part, 0, len(parts))
	for _, part := range parts {
		result = append(result, part)
	}
	slices.SortFunc(result, func(a, b Part) int { return a.Index - b.Index })
	return result
}

func sourceHash(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("abrir o áudio: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			digest.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			return hex.EncodeToString(digest.Sum(nil)), nil
		}
		if err != nil {
			return "", fmt.Errorf("ler o áudio: %w", err)
		}
	}
}

func fingerprint(hash string, settings config.Settings) (string, error) {
	return fingerprintWithModel(hash, settings, "")
}

func fingerprintWithModel(hash string, settings config.Settings, localModel string) (string, error) {
	return fingerprintWithPreparation(hash, settings, localModel, 0, 0, 0)
}

func fingerprintWithPreparation(hash string, settings config.Settings, localModel string, preparationVersion, streamPrompt int, nativeContext uint64) (string, error) {
	configuration := struct {
		AudioHash       string          `json:"audio_hash"`
		Model           string          `json:"model"`
		Region          string          `json:"region"`
		ChunkSeconds    int             `json:"chunk_seconds"`
		OverlapSeconds  float64         `json:"overlap_seconds"`
		Glossary        string          `json:"glossary"`
		SampleRate      int             `json:"sample_rate"`
		BitRate         int             `json:"bit_rate"`
		PromptVersion   int             `json:"prompt_version"`
		DetectorVersion int             `json:"speech_detector_version"`
		CacheVersion    int             `json:"cache_version"`
		Provider        string          `json:"provider,omitempty"`
		LocalModel      string          `json:"local_model,omitempty"`
		Whisper         *config.Whisper `json:"whisper,omitempty"`
		SpeakerLabels   bool            `json:"speaker_labels,omitempty"`
		Preparation     int             `json:"stream_preparation,omitempty"`
		StreamPrompt    int             `json:"stream_prompt,omitempty"`
		NativeContext   uint64          `json:"native_context_samples,omitempty"`
		CloudLanguage   string          `json:"cloud_language,omitempty"`
		API             any             `json:"api,omitempty"`
		Recovery        int             `json:"recovery_version,omitempty"`
		Fallback        *config.Whisper `json:"fallback_whisper,omitempty"`
		FallbackModel   string          `json:"fallback_model,omitempty"`
	}{
		AudioHash: hash, Model: settings.ModelID(), Region: settings.Region,
		ChunkSeconds: settings.ChunkSeconds, OverlapSeconds: settings.OverlapSeconds, Glossary: settings.Glossary,
		SampleRate: audio.SampleRate, BitRate: audio.BitRate, PromptVersion: config.TranscriptionPromptVersion,
		DetectorVersion: audio.SpeechDetectorVersion, CacheVersion: cacheVersion,
		SpeakerLabels: settings.SpeakerLabels,
		Preparation:   preparationVersion,
		StreamPrompt:  streamPrompt,
		NativeContext: nativeContext,
	}
	if settings.TranscriptionProvider() == config.ProviderWhisper {
		configuration.Provider = config.ProviderWhisper
		configuration.LocalModel = localModel
		configuration.Whisper = &settings.Whisper
		configuration.Region = "local"
	} else if settings.TranscriptionProvider() == config.ProviderAPI {
		configuration.Provider = config.ProviderAPI
		configuration.API = settings.AudioAPI
		configuration.Region = "api"
	} else if model, _ := config.CloudModelByID(settings.CloudModelID()); model.Backend == config.CloudTranscribe || model.Backend == config.CloudStreaming {
		configuration.CloudLanguage = settings.Cloud.TranscribeLanguage()
	}
	if settings.SupportsSubdivision() && streamPrompt == 0 {
		configuration.Recovery = 1
		if settings.VoxtralRecovery() && !settings.Recovery.WhisperDisabled {
			configuration.Fallback, configuration.FallbackModel = &settings.Whisper, localModel
		}
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		return "", fmt.Errorf("identificar a configuração da transcrição: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func supported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".amr", ".flac", ".m4a", ".mp3", ".mp4",
		".oga", ".ogg", ".opus", ".wav", ".webm", ".wma":
		return true
	}
	return false
}
