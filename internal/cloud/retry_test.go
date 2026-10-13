package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func TestCloudErrorRetriesOnlyTemporaryFailures(t *testing.T) {
	const secret = "FAKE-PRIVATE-DIAGNOSTIC"
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "model timeout", err: &types.ModelTimeoutException{}, want: true},
		{name: "service unavailable", err: &smithy.GenericAPIError{Code: "ServiceUnavailableException"}, want: true},
		{name: "throttle", err: &smithy.GenericAPIError{Code: "ThrottlingException"}, want: true},
		{name: "transient model HTTP", err: &types.ModelErrorException{OriginalStatusCode: aws.Int32(503)}, want: true},
		{name: "invalid model HTTP", err: &types.ModelErrorException{OriginalStatusCode: aws.Int32(422)}},
		{name: "interrupted response", err: io.ErrUnexpectedEOF, want: true},
		{name: "network failure", err: &net.OpError{Op: "read", Err: errors.New(secret)}, want: true},
		{name: "cancel", err: context.Canceled},
		{name: "expired login", err: &smithy.GenericAPIError{Code: "ExpiredTokenException", Message: secret}},
		{name: "permission", err: &smithy.GenericAPIError{Code: "AccessDeniedException", Fault: smithy.FaultServer, Message: secret}},
		{name: "invalid request", err: &smithy.GenericAPIError{Code: "ValidationException", Message: secret}},
		{name: "certificate", err: errors.New("certificate expired " + secret)},
		{name: "credential deadline", err: &credentialFailure{message: "Falha de login", cause: context.DeadlineExceeded}},
		{name: "output limit", err: errors.Join(ErrIncompleteResponse, ErrOutputLimit)},
		{name: "unknown", err: errors.New(secret)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := readableError(fmt.Errorf("operation: %w", tc.err), "mock-profile")
			var cloudErr *Error
			if !errors.As(err, &cloudErr) || cloudErr.Retryable() != tc.want {
				t.Fatalf("retryable=%t, want %t: %v", cloudErr != nil && cloudErr.Retryable(), tc.want, err)
			}
			if !errors.Is(err, tc.err) || strings.Contains(err.Error(), secret) {
				t.Fatal("retry classification lost its cause or exposed diagnostics")
			}
		})
	}
}

func TestVoxtralSDKDoesNotMultiplyApplicationRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-amzn-errortype", "ServiceUnavailableException")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"message":"temporary test failure"}`)
	}))
	defer server.Close()
	client := bedrockruntime.NewFromConfig(aws.Config{
		Region: "us-east-1", HTTPClient: server.Client(),
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "MOCK", SecretAccessKey: "MOCK"}, nil
		}),
		Retryer: func() aws.Retryer { return retry.NewStandard() },
	}, func(options *bedrockruntime.Options) { options.BaseEndpoint = aws.String(server.URL) })
	transcriber, err := NewWithClient(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	_, err = transcriber.Transcribe(t.Context(), spokenChunk(120))
	var failure *Error
	if !errors.As(err, &failure) || !failure.Retryable() || calls.Load() != 1 {
		t.Fatalf("one application attempt became multiple SDK calls: calls=%d, error=%v", calls.Load(), err)
	}
}

func TestVoxtralLongAudioHasEnoughTimeAndStillHonoursCallerDeadline(t *testing.T) {
	for _, seconds := range []float64{120, 300} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			want := time.Duration(min(360, seconds*2+30)) * time.Second
			client := converseFunc(func(ctx context.Context, _ *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				deadline, ok := ctx.Deadline()
				if left := time.Until(deadline); !ok || left > want || left < want-time.Second {
					t.Errorf("long audio still has a fixed two-minute deadline: %v, want %v", left, want)
				}
				return completeResponse("Texto completo.", types.StopReasonEndTurn), nil
			})
			transcriber, err := NewWithClient(config.Default(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer transcriber.Close()
			if _, err := transcriber.Transcribe(t.Context(), spokenChunk(seconds)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
