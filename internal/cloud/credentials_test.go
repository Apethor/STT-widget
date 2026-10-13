package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/processcreds"
)

func isolateAWS(t *testing.T, contents string) {
	t.Helper()
	directory := t.TempDir()
	configuration := filepath.Join(directory, "aws-config")
	credentials := filepath.Join(directory, "aws-credentials")
	if err := os.WriteFile(configuration, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_CREDENTIAL_EXPIRATION", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_CA_BUNDLE",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
		"AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_STS", "AWS_ENDPOINT_URL_BEDROCK_RUNTIME",
		"AWS_IGNORE_CONFIGURED_ENDPOINT_URLS",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_CONFIG_FILE", configuration)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_DEFAULTS_MODE", "standard")
}

type helperRecord struct {
	VisibleConsole bool     `json:"visible_console"`
	Arguments      []string `json:"arguments"`
	PID            int      `json:"pid"`
}

func prepareCredentialHelper(t *testing.T) (command, record string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record = filepath.Join(t.TempDir(), "credential helper invocations.jsonl")
	t.Setenv("AI_TALKER_CLOUD_TEST_HELPER", "1")
	t.Setenv("AI_TALKER_CLOUD_TEST_RECORD", record)
	t.Setenv("AI_TALKER_CLOUD_TEST_MODE", "valid")
	t.Setenv("AI_TALKER_CLOUD_TEST_EXPIRATION", "")
	// Pass this exact command through the official AWS shell builder, including
	// its executable path quoting. Arguments and environment contain no secrets.
	command = `"` + executable + `" -test.run=^TestCredentialHelperProcess$ -- aws-test-helper`
	return command, record
}

func helperRecords(t *testing.T, filename string) []helperRecord {
	t.Helper()
	payload, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []helperRecord
	for _, line := range bytes.Split(bytes.TrimSpace(payload), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record helperRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

// TestCredentialHelperProcess runs only in child copies of the test binary. It
// never reads a user's AWS files or calls AWS, and persists only process metadata.
func TestCredentialHelperProcess(t *testing.T) {
	if os.Getenv("AI_TALKER_CLOUD_TEST_HELPER") != "1" {
		return
	}
	file, err := os.OpenFile(os.Getenv("AI_TALKER_CLOUD_TEST_RECORD"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(20)
	}
	if err := json.NewEncoder(file).Encode(helperRecord{
		VisibleConsole: credentialHelperVisibleConsole(),
		Arguments:      os.Args[1:],
		PID:            os.Getpid(),
	}); err != nil {
		os.Exit(21)
	}
	if err := file.Close(); err != nil {
		os.Exit(22)
	}
	switch os.Getenv("AI_TALKER_CLOUD_TEST_MODE") {
	case "wait":
		time.Sleep(30 * time.Second)
	case "expired":
		if _, err := fmt.Fprintln(os.Stderr, "SSO token expired DO-NOT-LOG-HELPER-DIAGNOSTIC"); err != nil {
			os.Exit(23)
		}
		os.Exit(1)
	case "malformed":
		if _, err := fmt.Fprint(os.Stdout, `{"Version":1,"SecretAccessKey":"DO-NOT-LOG-HELPER-DIAGNOSTIC",bad`); err != nil {
			os.Exit(24)
		}
		os.Exit(0)
	}
	expiration := os.Getenv("AI_TALKER_CLOUD_TEST_EXPIRATION")
	if expiration == "" {
		expiration = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"Version":         1,
		"AccessKeyId":     "CLOUD-TEST-KEY",
		"SecretAccessKey": "cloud-test-secret",
		"SessionToken":    "cloud-test-token",
		"Expiration":      expiration,
	}); err != nil {
		os.Exit(25)
	}
	os.Exit(0)
}

func TestCredentialHelperCommand(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	defaultBuilder := processcreds.DefaultNewCommandBuilder{Args: []string{command}}
	builder := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
		func(ctx context.Context) (*exec.Cmd, error) {
			cmd, err := defaultBuilder.NewCommand(ctx)
			if err != nil {
				return nil, err
			}
			configureCredentialCommand(ctx, cmd, command)
			return cmd, nil
		},
	)}
	cmd, err := builder.NewCommand(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		// Only this synthetic helper's stderr is included. Never display the
		// credential JSON from stdout, including in a failed test.
		t.Fatalf("synthetic helper command: %v; stderr: %s", err, diagnostics.String())
	}
	var response processcreds.CredentialProcessResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal("synthetic helper did not return credential JSON:", err)
	}
	if response.Version != 1 || len(helperRecords(t, record)) != 1 {
		t.Fatal("synthetic credential helper did not run as requested")
	}
}

func TestLoadAWSConfigLimits(t *testing.T) {
	isolateAWS(t, "[default]\naws_access_key_id = CLOUD-TEST-KEY\naws_secret_access_key = cloud-test-secret\n")
	// The app's explicit bounds take precedence over ambient SDK retry settings.
	t.Setenv("AWS_MAX_ATTEMPTS", "12")
	t.Setenv("AWS_RETRY_MODE", "adaptive")
	cfg, closeHTTP, err := loadAWSConfig(t.Context(), config.Default(), callTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	if cfg.Region != "us-east-1" || cfg.RetryMaxAttempts != 2 || cfg.RetryMode != aws.RetryModeStandard {
		t.Errorf("region = %s, retry max = %d, retry mode = %s", cfg.Region, cfg.RetryMaxAttempts, cfg.RetryMode)
	}
	client, ok := cfg.HTTPClient.(*http.Client)
	if !ok || client.Timeout != callTimeout {
		t.Fatal("SDK requests do not have the configured HTTP timeout")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSHandshakeTimeout != connectTimeout || transport.ResponseHeaderTimeout != callTimeout {
		t.Fatal("connection and response timeouts were lost")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification was disabled")
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal("signed requests may follow redirects")
	}
}

func TestCredentialProcessInitialAndRefresh(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
	t.Setenv("AI_TALKER_CLOUD_TEST_EXPIRATION", "2000-01-01T00:00:00Z")
	settings := config.Default()
	settings.Profile = "desktop-test"
	cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	for range 2 {
		credentials, err := cfg.Credentials.Retrieve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if credentials.AccessKeyID != "CLOUD-TEST-KEY" || !credentials.CanExpire || !credentials.Expired() {
			t.Fatal("helper credentials or expiration were not preserved")
		}
	}
	calls := helperRecords(t, record)
	if len(calls) != 2 {
		t.Fatalf("helper ran %d times, want initial retrieval and automatic refresh", len(calls))
	}
	for _, call := range calls {
		if call.VisibleConsole {
			t.Error("credential helper created a visible console")
		}
		if !slices.Contains(call.Arguments, "aws-test-helper") {
			t.Error("SDK credential command lost its arguments")
		}
	}
}

func TestCredentialCacheOptions(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
	settings := config.Default()
	settings.Profile = "desktop-test"
	cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout,
		awsconfig.WithCredentialsCacheOptions(func(o *aws.CredentialsCacheOptions) {
			// The fake helper expires in one hour. This SDK option forces an
			// early refresh, and must survive replacement of the process branch.
			o.ExpiryWindow = 2 * time.Hour
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	for range 2 {
		if _, err := cfg.Credentials.Retrieve(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(helperRecords(t, record)); got != 2 {
		t.Errorf("cache option was lost; helper runs = %d, want 2", got)
	}
}

func TestAWSProfilePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		profile    string
		wantKey    string
		helperRuns int
	}{
		{name: "environment credentials in default chain", wantKey: "CLOUD-ENV-TEST-KEY"},
		{name: "explicit profile overrides environment credentials", profile: "desktop-test", wantKey: "CLOUD-TEST-KEY", helperRuns: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command, record := prepareCredentialHelper(t)
			isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
			t.Setenv("AWS_PROFILE", "desktop-test")
			t.Setenv("AWS_ACCESS_KEY_ID", "CLOUD-ENV-TEST-KEY")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "cloud-env-test-secret")
			settings := config.Default()
			settings.Profile = tc.profile
			cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer closeHTTP()
			credentials, err := cfg.Credentials.Retrieve(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if credentials.AccessKeyID != tc.wantKey {
				t.Errorf("wrong selected credential source for %s", tc.name)
			}
			if got := len(helperRecords(t, record)); got != tc.helperRuns {
				t.Errorf("helper runs = %d, want %d", got, tc.helperRuns)
			}
		})
	}
}

func TestSSOAndSourceProfileSelection(t *testing.T) {
	isolateAWS(t, `[profile sso-test]
sso_session = cloud-unit-test
sso_account_id = 111111111111
sso_role_name = TestRole
credential_process = helper-must-never-run
[profile role-test]
role_arn = arn:aws:iam::111111111111:role/TestRole
source_profile = sso-test
[sso-session cloud-unit-test]
sso_start_url = https://cloud-unit-test.awsapps.com/start
sso_region = us-east-1
`)
	for _, profile := range []string{"sso-test", "role-test"} {
		t.Run(profile, func(t *testing.T) {
			settings := config.Default()
			settings.Profile = profile
			cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer closeHTTP()
			// Do not Retrieve: SSO authentication stays with the official SDK
			// and no real cached SSO token or AWS service is touched.
			source, ok := cfg.Credentials.(aws.CredentialProviderSource)
			if !ok {
				t.Fatal("SDK SSO provider has no credential source metadata")
			}
			sources := source.ProviderSources()
			hasSSO := slices.Contains(sources, aws.CredentialSourceSSO) ||
				slices.Contains(sources, aws.CredentialSourceSSOLegacy)
			if !hasSSO || slices.Contains(sources, aws.CredentialSourceProcess) {
				t.Fatal("SDK SSO selection was replaced by credential_process")
			}
			if profile == "role-test" &&
				!slices.Contains(source.ProviderSources(), aws.CredentialSourceProfileSourceProfile) {
				t.Fatal("SSO source_profile chain was discarded")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func stsResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/xml"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func assumeRoleResponse(key, role string) string {
	return `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult>` +
		`<Credentials><AccessKeyId>` + key + `</AccessKeyId><SecretAccessKey>cloud-role-test-secret</SecretAccessKey>` +
		`<SessionToken>cloud-role-test-token</SessionToken><Expiration>2040-01-01T00:00:00Z</Expiration></Credentials>` +
		`<AssumedRoleUser><Arn>` + role + `</Arn><AssumedRoleId>TEST:session</AssumedRoleId></AssumedRoleUser>` +
		`</AssumeRoleResult><ResponseMetadata><RequestId>cloud-test</RequestId></ResponseMetadata></AssumeRoleResponse>`
}

func TestCredentialProcessSourceProfileRoles(t *testing.T) {
	command, record := prepareCredentialHelper(t)
	isolateAWS(t, `[profile outer]
role_arn = arn:aws:iam::111111111111:role/Outer
source_profile = inner
role_session_name = outer-session
external_id = outer-external
duration_seconds = 3600
[profile inner]
role_arn = arn:aws:iam::111111111111:role/Inner
source_profile = helper
role_session_name = inner-session
external_id = inner-external
duration_seconds = 1800
[profile helper]
credential_process = `+command+"\n")
	settings := config.Default()
	settings.Profile = "outer"
	var requests []url.Values
	var signingKeys []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		requests = append(requests, values)
		signingKeys = append(signingKeys, request.Header.Get("Authorization"))
		key := "CLOUD-INNER-ROLE-KEY"
		if strings.HasSuffix(values.Get("RoleArn"), "/Outer") {
			key = "CLOUD-OUTER-ROLE-KEY"
		}
		return stsResponse(request, http.StatusOK, assumeRoleResponse(key, values.Get("RoleArn"))), nil
	})}
	cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout, awsconfig.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	credentials, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "CLOUD-OUTER-ROLE-KEY" || len(requests) != 2 {
		t.Fatalf("source_profile roles were lost; mock STS requests = %d", len(requests))
	}
	expected := []struct {
		role, session, external, duration, signingKey string
	}{
		{"Inner", "inner-session", "inner-external", "1800", "CLOUD-TEST-KEY"},
		{"Outer", "outer-session", "outer-external", "3600", "CLOUD-INNER-ROLE-KEY"},
	}
	for i, want := range expected {
		form := requests[i]
		if form.Get("Action") != "AssumeRole" || !strings.HasSuffix(form.Get("RoleArn"), "/"+want.role) ||
			form.Get("RoleSessionName") != want.session || form.Get("ExternalId") != want.external ||
			form.Get("DurationSeconds") != want.duration {
			t.Errorf("mock role request %d did not preserve profile options: %v", i, form)
		}
		if !strings.Contains(signingKeys[i], "Credential="+want.signingKey+"/") {
			t.Errorf("mock role request %d used the wrong credential source", i)
		}
	}
	if calls := helperRecords(t, record); len(calls) != 1 || calls[0].VisibleConsole {
		t.Fatalf("process source for the role chain was not hidden or cached; helper calls = %d", len(calls))
	}
}

func TestCredentialHelperErrorPrivacy(t *testing.T) {
	for _, mode := range []string{"expired", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			command, _ := prepareCredentialHelper(t)
			isolateAWS(t, "[profile desktop-test]\ncredential_process = "+command+"\n")
			t.Setenv("AI_TALKER_CLOUD_TEST_MODE", mode)
			settings := config.Default()
			settings.Profile = "desktop-test"
			cfg, closeHTTP, err := loadAWSConfig(t.Context(), settings, callTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer closeHTTP()
			_, err = cfg.Credentials.Retrieve(t.Context())
			if err == nil {
				t.Fatal("helper failure was accepted")
			}
			if strings.Contains(err.Error(), "DO-NOT-LOG-HELPER-DIAGNOSTIC") {
				t.Fatal("SDK credential cache exposed helper diagnostics")
			}
			userError := readableError(err, settings.Profile)
			if strings.Contains(userError.Error(), "DO-NOT-LOG-HELPER-DIAGNOSTIC") {
				t.Fatal("a user message exposed helper diagnostics")
			}
			if mode == "expired" && !strings.Contains(userError.Error(), "login AWS expirou") {
				t.Errorf("SSO expiration was not classified: %v", userError)
			}
			var processError *processcreds.ProviderError
			if !errors.As(userError, &processError) {
				t.Error("the SDK credential_process cause was lost")
			}
		})
	}
}

func TestHiddenCommandBuilder(t *testing.T) {
	t.Parallel()
	input := strings.NewReader("input")
	var output, diagnostics bytes.Buffer
	calls := 0
	builder := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
		func(ctx context.Context) (*exec.Cmd, error) {
			calls++
			cmd := exec.CommandContext(ctx, "helper-not-executed", "--json")
			cmd.Env = []string{"CLOUD_TEST_OPTION=preserved"}
			cmd.Dir = t.TempDir()
			cmd.Stdin = input
			cmd.Stdout = &output
			cmd.Stderr = &diagnostics
			cmd.WaitDelay = 7 * time.Second
			seedProcessOptions(cmd)
			return cmd, nil
		},
	)}
	for range 2 {
		cmd, err := builder.NewCommand(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertHiddenProcess(t, cmd)
		assertPreservedProcessOptions(t, cmd)
		if cmd.Stdin != input || cmd.Stdout != &output || cmd.Stderr != &diagnostics ||
			cmd.WaitDelay != 7*time.Second || cmd.Cancel == nil || cmd.Dir == "" ||
			!slices.Equal(cmd.Env, []string{"CLOUD_TEST_OPTION=preserved"}) ||
			!slices.Equal(cmd.Args, []string{"helper-not-executed", "--json"}) {
			t.Fatal("hiding the helper replaced its process options")
		}
	}
	if calls != 2 {
		t.Errorf("command builder ran %d times", calls)
	}

	cause := errors.New("command construction failed")
	failing := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
		func(context.Context) (*exec.Cmd, error) { return nil, cause },
	)}
	if _, err := failing.NewCommand(t.Context()); !errors.Is(err, cause) {
		t.Errorf("constructor cause = %v", err)
	}
	missing := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
		func(context.Context) (*exec.Cmd, error) { return nil, nil },
	)}
	if _, err := missing.NewCommand(t.Context()); err == nil {
		t.Error("nil command was accepted")
	}
}

func TestProcessTimeoutAndCancellation(t *testing.T) {
	for _, name := range []string{"provider timeout", "caller deadline", "caller cancellation"} {
		t.Run(name, func(t *testing.T) {
			prepareCredentialHelper(t)
			t.Setenv("AI_TALKER_CLOUD_TEST_MODE", "wait")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			builder := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
				func(ctx context.Context) (*exec.Cmd, error) {
					cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCredentialHelperProcess$")
					// Execute the helper directly for cancellation tests, so
					// killing it cannot leave a shell child behind.
					return cmd, nil
				},
			)}
			ctx := t.Context()
			var cancel context.CancelFunc
			processTimeout := 5 * time.Second
			switch name {
			case "provider timeout":
				processTimeout = 30 * time.Millisecond
			case "caller deadline":
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			case "caller cancellation":
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			provider := boundedProvider{
				CredentialsProvider: processcreds.NewProviderCommand(builder, func(o *processcreds.Options) {
					o.Timeout = processTimeout
				}),
				timeout: 3 * time.Second,
			}
			started := time.Now()
			_, err = provider.Retrieve(ctx)
			if err == nil {
				t.Fatal("timed out credential process was accepted")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("credential helper did not stop promptly")
			}
			switch name {
			case "provider timeout":
				if !strings.Contains(readableError(err, "").Error(), "processo de login") {
					t.Errorf("provider timeout message = %v", readableError(err, ""))
				}
			case "caller deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("caller deadline cause = %v", err)
				}
			case "caller cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Errorf("caller cancellation cause = %v", err)
				}
			}
		})
	}
}

func TestAWSCustomCABundle(t *testing.T) {
	isolateAWS(t, "[default]\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	bundle := filepath.Join(t.TempDir(), "test-ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CA_BUNDLE", bundle)
	cfg, closeHTTP, err := loadAWSConfig(t.Context(), config.Default(), callTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := cfg.HTTPClient.Do(request)
	if err != nil {
		t.Fatal("AWS_CA_BUNDLE was not applied to the SDK HTTP client:", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("mock TLS status = %d", response.StatusCode)
	}
}

func TestInvalidAWSCustomCABundle(t *testing.T) {
	isolateAWS(t, "[default]\n")
	bundle := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(bundle, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CA_BUNDLE", bundle)
	if _, err := New(t.Context(), config.Default()); err == nil || !strings.Contains(err.Error(), "AWS_CA_BUNDLE") {
		t.Errorf("invalid CA bundle message = %v", err)
	}
}
