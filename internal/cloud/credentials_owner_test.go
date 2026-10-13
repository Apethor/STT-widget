package cloud

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func sdkCredentials(t *testing.T, client any) aws.CredentialsProvider {
	t.Helper()
	transcriber, ok := client.(*transcriber)
	if !ok {
		t.Fatal("factory did not return its cloud implementation")
	}
	sdk, ok := transcriber.client.(*bedrockruntime.Client)
	if !ok {
		t.Fatal("factory did not use the official SDK")
	}
	return sdk.Options().Credentials
}

func TestCredentialOwnerSurvivesFactoryContext(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
	settings := config.Default()
	settings.Profile = "desktop-test"
	ctx, cancel := context.WithCancel(t.Context())
	client, err := New(ctx, settings)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	// New itself has already cancelled its temporary load context. Explicitly
	// cancelling its original foreground context must not poison later refreshes.
	credentials, err := sdkCredentials(t, client).Retrieve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.HasKeys() || len(helperRecords(t, record)) != 1 {
		t.Fatal("credentials could not be retrieved after factory cancellation")
	}
}

func TestCloseCancelsDetachedCredentialProcess(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
	t.Setenv("AI_TALKER_CLOUD_TEST_MODE", "wait")
	settings := config.Default()
	settings.Profile = "desktop-test"
	client, err := New(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	provider := sdkCredentials(t, client)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := provider.Retrieve(ctx)
		result <- err
	}()

	// Wait for the real, synthetic helper to begin its long sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		payload, err := os.ReadFile(record)
		if err == nil && strings.HasSuffix(string(payload), "\n") {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			select {
			case err := <-result:
				t.Fatalf("synthetic helper failed before starting: %v", err)
			default:
				t.Fatal("synthetic helper did not start")
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	pid := helperRecords(t, record)[0].PID
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("SDK cache cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SDK cache did not release its caller on cancellation")
	}

	// CredentialsCache intentionally left the retrieval running. Closing the
	// actual factory client must cancel its owner and join that background work.
	started := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("Close waited %s for the credential helper", elapsed)
	}
	assertHelperExited(t, pid)
	if _, err := provider.Retrieve(t.Context()); !errors.Is(err, context.Canceled) {
		t.Errorf("closed client allowed a new retrieval: %v", err)
	}
	if got := len(helperRecords(t, record)); got != 1 {
		t.Errorf("closed client launched another helper; invocations = %d", got)
	}
}

func TestCredentialOwnerRejectsWorkAfterClose(t *testing.T) {
	t.Parallel()
	owner := newCredentialOwner(t.Context())
	owner.close()
	if _, _, err := owner.begin(t.Context()); !errors.Is(err, context.Canceled) {
		t.Errorf("closed owner accepted work: %v", err)
	}
}
