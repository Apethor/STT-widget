package cloud

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

type firstTextTimer struct {
	started time.Time
	once    sync.Once
	mu      sync.Mutex
	elapsed float64
	stage   string
}

func (t *firstTextTimer) observe(text, stage string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	t.once.Do(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.elapsed = time.Since(t.started).Seconds()
		t.stage = stage
	})
}

func (t *firstTextTimer) wrap(callback func(string)) func(string) {
	if callback == nil {
		return nil
	}
	return func(text string) {
		t.observe(text, "partial")
		callback(text)
	}
}

func (t *firstTextTimer) record(usage map[string]any) map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stage == "" {
		return usage
	}
	if usage == nil {
		usage = make(map[string]any)
	}
	usage["first_text_seconds"] = t.elapsed
	usage["first_text_stage"] = t.stage
	usage["response_finalize_seconds"] = max(0, time.Since(t.started).Seconds()-t.elapsed)
	return usage
}

// Timings belong to one recognition call, even when its reusable client is
// shared by concurrent jobs. Credential lookup and stream sending may record
// phases concurrently; usage is copied only after the sender has stopped.
type requestTimings struct {
	mu        sync.Mutex
	durations map[string]float64
	counts    map[string]int
	firstText *firstTextTimer
}

type requestTimingsKey struct{}

func withRequestTimings(ctx context.Context) (context.Context, *requestTimings) {
	timings := &requestTimings{
		durations: make(map[string]float64),
		counts:    make(map[string]int),
	}
	return context.WithValue(ctx, requestTimingsKey{}, timings), timings
}

func timingsFromContext(ctx context.Context) *requestTimings {
	timings, _ := ctx.Value(requestTimingsKey{}).(*requestTimings)
	return timings
}

func measurePhase(ctx context.Context, name string) func() {
	started := time.Now()
	timings := timingsFromContext(ctx)
	var once sync.Once
	return func() {
		if timings == nil {
			return
		}
		once.Do(func() {
			timings.mu.Lock()
			defer timings.mu.Unlock()
			timings.durations[name] += time.Since(started).Seconds()
		})
	}
}

func countPhase(ctx context.Context, name string) {
	timings := timingsFromContext(ctx)
	if timings == nil {
		return
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	timings.counts[name]++
}

func observeFirstText(ctx context.Context, text, stage string) {
	if timings := timingsFromContext(ctx); timings != nil && timings.firstText != nil {
		timings.firstText.observe(text, stage)
	}
}

func (t *requestTimings) record(usage map[string]any) map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.durations) == 0 && len(t.counts) == 0 {
		return usage
	}
	if usage == nil {
		usage = make(map[string]any)
	}
	for name, seconds := range t.durations {
		usage[name] = seconds
	}
	for name, count := range t.counts {
		usage[name] = count
	}
	return usage
}

// Keep the SDK's cache and refresh lifecycle intact. This wrapper measures the
// duration of Retrieve (including a cache wait); it never stores credentials
// in transcription metadata or triggers an eager lookup.
type timedCredentialsProvider struct {
	provider aws.CredentialsProvider
}

func (p timedCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	finish := measurePhase(ctx, "credential_resolution_seconds")
	defer finish()
	countPhase(ctx, "credential_resolutions")
	return p.provider.Retrieve(ctx)
}

func (p timedCredentialsProvider) ProviderSources() []aws.CredentialSource {
	if source, ok := p.provider.(aws.CredentialProviderSource); ok {
		return source.ProviderSources()
	}
	return nil
}

func (p timedCredentialsProvider) IsCredentialsProvider(target aws.CredentialsProvider) bool {
	return aws.IsCredentialsProvider(p.provider, target)
}
