package cloud

import (
	"context"
	"net/http"
	"time"

	"ai-talker/internal/processing"
)

// PublicWebSearch gives alternative text providers the same source retrieval
// without credentials or a Bedrock grounding fallback.
func PublicWebSearch(ctx context.Context, query string) (string, []processing.Source, error) {
	client := &http.Client{
		Timeout:       publicSearchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	result, err := publicSearch(ctx, client, query, time.Now())
	return result.Text, result.Sources, err
}
