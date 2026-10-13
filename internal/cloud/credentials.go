package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/processcreds"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func loadAWSConfig(
	ctx context.Context,
	settings config.Settings,
	timeout time.Duration,
	extraOptions ...func(*awsconfig.LoadOptions) error,
) (aws.Config, func(), error) {
	if err := ctx.Err(); err != nil {
		return aws.Config{}, nil, err
	}
	httpClient := awshttp.NewBuildableClient().
		WithTimeout(timeout).
		WithDialerOptions(func(d *net.Dialer) { d.Timeout = connectTimeout }).
		WithTransportOptions(func(t *http.Transport) {
			t.TLSHandshakeTimeout = connectTimeout
			t.ResponseHeaderTimeout = timeout
		})
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(settings.Region),
		awsconfig.WithHTTPClient(httpClient),
		awsconfig.WithRetryMode(aws.RetryModeStandard),
		awsconfig.WithRetryMaxAttempts(2),
	}
	if settings.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(settings.Profile))
	}
	options = append(options, extraOptions...)
	// LoadDefaultConfig reads AWS_CA_BUNDLE (and a profile's ca_bundle) through
	// the SDK's custom-CA resolver. Keep the BuildableClient until that resolver
	// has installed its roots; passing a plain http.Client here breaks it.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return aws.Config{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return aws.Config{}, nil, err
	}

	closeHTTP := func() {}
	if configuredHTTP, ok := cfg.HTTPClient.(*awshttp.BuildableClient); ok {
		transport := configuredHTTP.GetTransport()
		cfg.HTTPClient = &http.Client{
			Transport: transport,
			Timeout:   timeout,
			// Bedrock and STS do not require redirects. Never forward a signed
			// request or session token to a redirected endpoint.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		closeHTTP = transport.CloseIdleConnections
	} else if closer, ok := cfg.HTTPClient.(interface{ CloseIdleConnections() }); ok {
		closeHTTP = closer.CloseIdleConnections
	}
	owner := newCredentialOwner(ctx)
	closeConfig := func() {
		owner.close()
		closeHTTP()
	}
	if err := hideCredentialProcesses(&cfg, timeout, owner); err != nil {
		closeConfig()
		return aws.Config{}, nil, err
	}
	if cfg.Credentials != nil {
		cfg.Credentials = timedCredentialsProvider{provider: cfg.Credentials}
	}
	return cfg, closeConfig, nil
}

func hideCredentialProcesses(cfg *aws.Config, timeout time.Duration, owner *credentialOwner) error {
	providerSource, ok := cfg.Credentials.(aws.CredentialProviderSource)
	if !ok || !slices.Contains(providerSource.ProviderSources(), aws.CredentialSourceProcess) {
		// Let the official resolver keep SSO, environment/static credentials,
		// web identity, ECS and IMDS chains exactly as it selected them. In
		// particular, SSO can take precedence over credential_process.
		return nil
	}

	var shared *awsconfig.SharedConfig
	var options awsconfig.LoadOptions
	for _, source := range cfg.ConfigSources {
		switch source := source.(type) {
		case awsconfig.SharedConfig:
			shared = &source
		case *awsconfig.SharedConfig:
			shared = source
		case awsconfig.LoadOptions:
			options = source
		case *awsconfig.LoadOptions:
			options = *source
		}
	}
	if shared == nil {
		return errors.New("o SDK não disponibilizou a configuração do credential_process")
	}
	provider, err := hiddenProcessChain(*cfg, shared, options, providerSource.ProviderSources(), timeout, owner)
	if err != nil {
		return err
	}
	cfg.Credentials = cachedProvider(provider, options.CredentialsCacheOptions)
	return nil
}

func hiddenProcessChain(
	cfg aws.Config,
	shared *awsconfig.SharedConfig,
	options awsconfig.LoadOptions,
	sources []aws.CredentialSource,
	timeout time.Duration,
	owner *credentialOwner,
) (aws.CredentialsProvider, error) {
	var provider aws.CredentialsProvider
	if shared.Source != nil {
		var err error
		provider, err = hiddenProcessChain(cfg, shared.Source, options, sources, timeout, owner)
		if err != nil {
			return nil, err
		}
	} else {
		if shared.CredentialProcess == "" {
			return nil, errors.New("a origem selecionada não disponibilizou o comando credential_process")
		}
		processOptions := processcreds.Options{Timeout: processcreds.DefaultTimeout}
		if options.ProcessCredentialOptions != nil {
			options.ProcessCredentialOptions(&processOptions)
		}
		processOptions.CredentialSources = slices.Clone(sources)
		provider = processProvider{command: shared.CredentialProcess, options: processOptions}
	}

	if shared.RoleARN != "" {
		// Rebuild only the SDK-selected process branch, including every role
		// along source_profile. Replacing the top-level credentials with just a
		// process provider would silently discard these assume-role hops.
		cfg.Credentials = cachedProvider(provider, options.CredentialsCacheOptions)
		provider = stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), shared.RoleARN,
			func(o *stscreds.AssumeRoleOptions) {
				o.RoleSessionName = shared.RoleSessionName
				if shared.RoleDurationSeconds != nil && *shared.RoleDurationSeconds/time.Minute > 15 {
					o.Duration = *shared.RoleDurationSeconds
				}
				if shared.ExternalID != "" {
					o.ExternalID = aws.String(shared.ExternalID)
				}
				if shared.MFASerial != "" {
					o.SerialNumber = aws.String(shared.MFASerial)
				}
				o.CredentialSources = slices.Clone(sources)
				if options.AssumeRoleCredentialOptions != nil {
					options.AssumeRoleCredentialOptions(o)
				}
			},
		)
	}
	return boundedProvider{CredentialsProvider: provider, timeout: timeout, owner: owner}, nil
}

func cachedProvider(provider aws.CredentialsProvider, options func(*aws.CredentialsCacheOptions)) *aws.CredentialsCache {
	if options != nil {
		return aws.NewCredentialsCache(provider, options)
	}
	return aws.NewCredentialsCache(provider)
}

type processProvider struct {
	command string
	options processcreds.Options
}

func (p processProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	// Each invocation owns its stderr buffer. Helpers often report expired SSO
	// sessions there; classify that failure without logging or retaining their
	// potentially sensitive diagnostics.
	var diagnostics bytes.Buffer
	defaultBuilder := processcreds.DefaultNewCommandBuilder{Args: []string{p.command}}
	builder := hiddenCommandBuilder{inner: processcreds.NewCommandBuilderFunc(
		func(ctx context.Context) (*exec.Cmd, error) {
			cmd, err := defaultBuilder.NewCommand(ctx)
			if err != nil {
				return nil, err
			}
			configureCredentialCommand(ctx, cmd, p.command)
			cmd.Stderr = &diagnostics
			return cmd, nil
		},
	)}
	provider := processcreds.NewProviderCommand(builder, func(o *processcreds.Options) { *o = p.options })
	credentials, err := provider.Retrieve(ctx)
	if err != nil && expiredLogin("", strings.ToLower(diagnostics.String())) {
		return aws.Credentials{}, &credentialFailure{
			message: "O login SSO AWS expirou.",
			cause:   err,
		}
	}
	return credentials, err
}

func (p processProvider) ProviderSources() []aws.CredentialSource {
	return slices.Clone(p.options.CredentialSources)
}

// hiddenCommandBuilder decorates each SDK command, so both the initial retrieval
// and every refresh retain their context, environment, pipes and process options.
type hiddenCommandBuilder struct {
	inner processcreds.NewCommandBuilder
}

func (b hiddenCommandBuilder) NewCommand(ctx context.Context) (*exec.Cmd, error) {
	cmd, err := b.inner.NewCommand(ctx)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, errors.New("o construtor de credenciais não retornou um processo")
	}
	platform.Hide(cmd)
	// A shell child can inherit stdout/stderr. Bound waiting for its pipes after
	// CommandContext kills the shell at cancellation or the provider's timeout.
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = time.Second
	}
	return cmd, nil
}

type boundedProvider struct {
	aws.CredentialsProvider
	timeout time.Duration
	owner   *credentialOwner
}

func (p boundedProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	if p.owner != nil {
		var done func()
		var err error
		ctx, done, err = p.owner.begin(ctx)
		if err != nil {
			return aws.Credentials{}, err
		}
		defer done()
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	credentials, err := p.CredentialsProvider.Retrieve(ctx)
	if err != nil && ctx.Err() != nil {
		err = errors.Join(ctx.Err(), err)
	}
	if err != nil {
		// Keep raw process diagnostics out of the message used by the SDK's
		// credential cache, while retaining the cause for classification.
		return aws.Credentials{}, &credentialFailure{
			message: "Não foi possível renovar as credenciais AWS.",
			cause:   fmt.Errorf("obter credenciais AWS: %w", err),
		}
	}
	return credentials, nil
}

func (p boundedProvider) ProviderSources() []aws.CredentialSource {
	if source, ok := p.CredentialsProvider.(aws.CredentialProviderSource); ok {
		return source.ProviderSources()
	}
	return nil
}

type credentialFailure struct {
	message string
	cause   error
}

func (e *credentialFailure) Error() string { return e.message }

func (e *credentialFailure) Unwrap() error { return e.cause }
