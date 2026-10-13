package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/realtime"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// NewConversation starts Nova speech-to-speech, independently of the selected
// transcription engine. The session reuses the application's AWS profile,
// trusted certificates and hidden credential-process handling.
func NewConversation(ctx context.Context, settings config.Settings, options realtime.Options, onEvent func(realtime.Event)) (*realtime.Session, error) {
	if ctx == nil {
		return nil, errors.New("informe o contexto da conversa ao vivo")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.ModelID == "" {
		options.ModelID = realtime.Nova25
	}
	if options.ModelID != realtime.Nova2 && options.ModelID != realtime.Nova25 {
		return nil, realtime.ErrUnsupportedProvider
	}
	if err := realtime.ValidateOptions(options); err != nil {
		return nil, err
	}
	if !realtime.SupportsModelRegion(options.ModelID, settings.Region) {
		return nil, fmt.Errorf("Nova Sonic conversa ao vivo está disponível em us-east-1, us-west-2, eu-north-1 e ap-northeast-1; região atual: %s", settings.Region)
	}
	// A continuous session must not inherit the bounded transcription HTTP
	// timeout. Opening and refreshing credentials remain bounded separately.
	cfg, cleanup, err := loadAWSConfig(ctx, settings, 30*time.Second, awsconfig.WithRetryMaxAttempts(1))
	if err != nil {
		return nil, readableError(err, settings.Profile)
	}
	// The existing resolver bounds credentials and response headers at 30 s.
	// Only the overall HTTP body lifetime changes for this continuous stream.
	if httpClient, ok := cfg.HTTPClient.(*http.Client); ok {
		copyClient := *httpClient
		copyClient.Timeout = 0
		cfg.HTTPClient = &copyClient
	}
	client := bedrockruntime.NewFromConfig(cfg)
	response, err := client.InvokeModelWithBidirectionalStream(ctx,
		&bedrockruntime.InvokeModelWithBidirectionalStreamInput{ModelId: aws.String(options.ModelID)})
	if err != nil {
		cleanup()
		return nil, readableError(err, settings.Profile)
	}
	if response == nil || response.GetStream() == nil {
		cleanup()
		return nil, errors.New("Nova Sonic não abriu uma sessão bidirecional")
	}
	stream := &conversationStream{Stream: response.GetStream(), cleanup: cleanup}
	session, err := realtime.New(ctx, options, stream, onEvent)
	if err != nil {
		_ = stream.Close()
		return nil, readableError(err, settings.Profile)
	}
	return session, nil
}

type conversationStream struct {
	realtime.Stream
	cleanup func()
	once    sync.Once
}

func (s *conversationStream) Close() error {
	err := s.Stream.Close()
	s.once.Do(s.cleanup)
	return err
}
