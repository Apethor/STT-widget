package cloud

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials/processcreds"
	"github.com/aws/smithy-go"
)

func TestReadableError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cause   error
		want    string
		profile string
	}{
		{name: "expired token", cause: &smithy.GenericAPIError{Code: "ExpiredTokenException"}, want: "login AWS expirou", profile: "desktop-test"},
		{name: "SSO refresh", cause: errors.New("SSO token refresh failed"), want: "login AWS expirou"},
		{name: "invalid SSO grant", cause: &smithy.GenericAPIError{Code: "InvalidGrantException"}, want: "login AWS expirou"},
		{name: "access denied", cause: &smithy.GenericAPIError{Code: "AccessDeniedException"}, want: "bedrock:InvokeModel"},
		{name: "model unavailable", cause: &smithy.GenericAPIError{Code: "ResourceNotFoundException"}, want: "ID e a região"},
		{name: "invalid model request", cause: &smithy.GenericAPIError{Code: "ValidationException"}, want: "outro modelo"},
		{name: "throttling", cause: &smithy.GenericAPIError{Code: "ThrottlingException"}, want: "limitou as chamadas"},
		{name: "service unavailable", cause: &smithy.GenericAPIError{Code: "ServiceUnavailableException"}, want: "temporariamente indisponível"},
		{name: "model loading", cause: &smithy.GenericAPIError{Code: "ModelNotReadyException"}, want: "progresso está salvo"},
		{name: "network", cause: &net.DNSError{Err: "no such host", Name: "bedrock.invalid"}, want: "conectar à AWS"},
		{name: "certificate", cause: x509.UnknownAuthorityError{}, want: "AWS_CA_BUNDLE"},
		{name: "expired certificate", cause: errors.New("certificate expired"), want: "AWS_CA_BUNDLE"},
		{name: "CA bundle", cause: errors.New("failed to load custom CA bundle PEM file"), want: "AWS_CA_BUNDLE"},
		{name: "cancelled", cause: context.Canceled, want: "cancelada"},
		{name: "deadline", cause: context.DeadlineExceeded, want: "demorou demais"},
		{name: "process timeout", cause: &processcreds.ProviderError{Err: errors.New("credential process timed out")}, want: "processo de login"},
		{name: "unknown failure", cause: errors.New("private diagnostic"), want: "Falha na AWS"},
		{
			name: "hidden credential failure retains expired classification",
			cause: &credentialFailure{
				message: "Não foi possível renovar as credenciais AWS.",
				cause: &credentialFailure{
					message: "O login SSO AWS expirou.",
					cause:   errors.New("exit status 1"),
				},
			},
			want: "login AWS expirou",
		},
		{name: "joined cancellation", cause: errors.Join(context.Canceled, errors.New("exit status 1")), want: "cancelada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("SDK operation: %w", tc.cause)
			got := readableError(wrapped, tc.profile)
			if !errors.Is(got, tc.cause) || !errors.Is(got, wrapped) {
				t.Error("the original cause was lost")
			}
			var cloudError *Error
			if !errors.As(got, &cloudError) {
				t.Fatalf("error type = %T, want *cloud.Error", got)
			}
			if !strings.Contains(got.Error(), tc.want) {
				t.Errorf("message = %q, want %q", got.Error(), tc.want)
			}
			if strings.Contains(tc.want, "login AWS expirou") {
				wantProfile := tc.profile
				if wantProfile == "" {
					wantProfile = "cadeia padrão da AWS"
				}
				if !strings.Contains(got.Error(), wantProfile) {
					t.Errorf("expired login message lacks profile %q", wantProfile)
				}
			}
		})
	}
}

func TestReadableErrorDoesNotExposeCredentials(t *testing.T) {
	t.Parallel()
	const privateValue = "DO-NOT-LOG-THIS-FAKE-CREDENTIAL"
	cases := []struct {
		name  string
		cause error
	}{
		{name: "process output", cause: &processcreds.ProviderError{Err: fmt.Errorf("parse failed of process output: %s", privateValue)}},
		{name: "AWS message", cause: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: privateValue}},
		{name: "network details", cause: &net.DNSError{Err: privateValue}},
		{name: "unknown error", cause: errors.New(privateValue)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := readableError(tc.cause, "desktop-test")
			if strings.Contains(got.Error(), privateValue) {
				t.Fatal("a user-facing error exposed credential diagnostics")
			}
			if !errors.Is(got, tc.cause) {
				t.Error("redaction lost the underlying error")
			}
		})
	}
	if readableError(nil, "") != nil {
		t.Error("nil error was not preserved")
	}
}
