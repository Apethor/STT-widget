package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"ai-talker/internal/transcript"
	"github.com/aws/aws-sdk-go-v2/credentials/processcreds"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// Error exposes a message suitable for the desktop while preserving the SDK's
// cause for errors.Is and errors.As. SDK and credential_process diagnostics can
// contain credentials, so Error never includes their raw contents.
type Error struct {
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Cause }

// Retryable distinguishes temporary service/transport failures from login,
// permission, certificate, input and incomplete-output failures.
func (e *Error) Retryable() bool {
	err := e.Cause
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrIncompleteResponse) {
		return false
	}
	var credentials *credentialFailure
	var process *processcreds.ProviderError
	if errors.As(err, &credentials) || errors.As(err, &process) {
		return false
	}
	code := ""
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		code = apiError.ErrorCode()
	}
	detail := diagnosticText(err)
	if certificateError(err, detail) || expiredLogin(code, detail) {
		return false
	}
	if apiError != nil {
		switch code {
		case "Throttling", "ThrottlingException", "TooManyRequestsException",
			"ServiceUnavailableException", "ModelNotReadyException", "ModelTimeoutException",
			"InternalServerException", "InternalFailure", "RequestTimeout",
			"RequestTimeoutException", "ModelStreamErrorException":
			return true
		case "ModelErrorException":
			var modelError *types.ModelErrorException
			return errors.As(err, &modelError) && modelError.OriginalStatusCode != nil &&
				retryableStatus(int(*modelError.OriginalStatusCode))
		case "AccessDenied", "AccessDeniedException", "UnauthorizedException",
			"ValidationException", "ResourceNotFoundException", "ServiceQuotaExceededException",
			"BadRequestException", "InvalidSignatureException":
			return false
		default:
			return apiError.ErrorFault() == smithy.FaultServer
		}
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		return retryableStatus(status.HTTPStatusCode())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return networkError(err)
}

func retryableStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500 && status < 600
}

// ErrIncompleteResponse means that AWS did not return a complete, textual
// transcription. Such a response must not be saved as a completed part.
var ErrIncompleteResponse = errors.New("resposta incompleta do motor AWS")

// ErrOutputLimit identifies a response cut off by the output token budget.
var ErrOutputLimit = transcript.ErrOutputLimit

func readableError(err error, profile string) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}

	message := "Falha na AWS. Confira o perfil, a região e a conexão e tente novamente."
	code := ""
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		code = apiError.ErrorCode()
	}
	// Inspect diagnostics only to classify the failure; never copy them into a
	// user message or log, particularly malformed credential_process output.
	detail := diagnosticText(err)
	var credentials *credentialFailure
	switch {
	case errors.Is(err, context.Canceled):
		message = "A operação na AWS foi cancelada."
	case errors.As(err, &credentials) && errors.Is(err, context.DeadlineExceeded):
		message = "O processo de login AWS demorou demais para responder. Renove o login e tente novamente."
	case errors.Is(err, context.DeadlineExceeded) || code == "ModelTimeoutException" ||
		code == "RequestTimeout" || code == "RequestTimeoutException":
		message = "A AWS demorou demais para responder. Confira a conexão e tente novamente."
	case certificateError(err, detail):
		message = "A conexão não reconheceu o certificado da rede. " +
			"Configure AWS_CA_BUNDLE com o certificado confiável."
	case expiredLogin(code, detail):
		if profile == "" {
			profile = "cadeia padrão da AWS"
		}
		message = "Seu login AWS expirou. Renove o login SSO de origem e tente novamente. " +
			"Perfil selecionado: " + profile + "."
	case code == "AccessDenied" || code == "AccessDeniedException" || code == "UnauthorizedException":
		message = "O perfil AWS não tem acesso a este modelo ou à operação bedrock:InvokeModel."
	case code == "ValidationException" || code == "ResourceNotFoundException":
		message = "O modelo não aceitou este pedido. Confira o ID e a região, ou selecione outro modelo."
	case code == "Throttling" || code == "ThrottlingException" ||
		code == "TooManyRequestsException" || code == "ServiceUnavailableException" ||
		code == "ModelNotReadyException":
		message = "A AWS está temporariamente indisponível ou limitou as chamadas. " +
			"Tente novamente; o progresso está salvo."
	case networkError(err):
		message = "Não foi possível conectar à AWS. Confira a conexão, o proxy e a região e tente novamente."
	case strings.Contains(detail, "credential process timed out") ||
		strings.Contains(detail, "credential_process timed out"):
		message = "O processo de login AWS demorou demais para responder. Renove o login e tente novamente."
	}
	return &Error{Message: message, Cause: err}
}

func diagnosticText(err error) string {
	var text strings.Builder
	for err != nil {
		text.WriteString(err.Error())
		text.WriteByte('\n')
		if multiple, ok := err.(interface{ Unwrap() []error }); ok {
			for _, cause := range multiple.Unwrap() {
				text.WriteString(diagnosticText(cause))
			}
			break
		}
		err = errors.Unwrap(err)
	}
	return strings.ToLower(text.String())
}

func expiredLogin(code, detail string) bool {
	switch code {
	case "ExpiredToken", "ExpiredTokenException", "UnrecognizedClientException",
		"InvalidClientTokenId", "InvalidIdentityToken", "UnauthorizedSSOTokenError":
		return true
	}
	for _, fragment := range []string{
		"expired", "sso token", "unauthorizedssotoken", "refresh failed",
		"invalid_grant", "invalidgrant", "login sso aws expirou", "sessão sso expirou",
	} {
		if strings.Contains(detail, fragment) {
			return true
		}
	}
	return false
}

func certificateError(err error, detail string) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var hostnameError x509.HostnameError
	var verificationError *tls.CertificateVerificationError
	return errors.As(err, &unknownAuthority) ||
		errors.As(err, &invalidCertificate) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &verificationError) ||
		strings.Contains(detail, "certificate") ||
		strings.Contains(detail, "ca bundle") ||
		strings.Contains(detail, "aws_ca_bundle") ||
		strings.Contains(detail, "ca_bundle")
}

func networkError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError)
}

func incompleteResponse(reason string) error {
	message := "A AWS não retornou o texto desta parte. Tente novamente."
	cause := fmt.Errorf("motivo de término %q: %w", reason, ErrIncompleteResponse)
	if reason == "max_tokens" {
		message = "A transcrição desta parte atingiu o limite de texto do modelo. " +
			"Tente novamente com partes menores nas configurações."
		cause = errors.Join(cause, ErrOutputLimit)
	} else if reason != "" {
		message = "A AWS não concluiu a transcrição desta parte (" + reason + "). " +
			"Reduza a duração das partes nas configurações e tente novamente."
	}
	return &Error{
		Message: message,
		Cause:   cause,
	}
}
