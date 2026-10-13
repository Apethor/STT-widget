package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type bucketCheckIdentity struct{}

func (bucketCheckIdentity) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
}

type bucketCheckObjects struct {
	t         *testing.T
	calls     []string
	payload   []byte
	key       string
	region    string
	fail      string
	cancel    context.CancelFunc
	deleteErr error
	owner     string
	versionID string
}

func (m *bucketCheckObjects) HeadBucket(_ context.Context, in *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	m.calls = append(m.calls, "head")
	m.owner = aws.ToString(in.ExpectedBucketOwner)
	if m.owner != "" {
		m.t.Fatal("a conta autenticada foi presumida como proprietária do bucket")
	}
	if m.fail == "head" {
		return nil, errors.New("HeadBucket: AccessDenied")
	}
	return &s3.HeadBucketOutput{BucketRegion: aws.String(m.region)}, nil
}

func (m *bucketCheckObjects) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.t.Helper()
	m.calls = append(m.calls, "put")
	m.key = aws.ToString(in.Key)
	if in.ServerSideEncryption != "" || in.SSEKMSKeyId != nil || !strings.HasPrefix(m.key, "ai-talker/diagnostics/") ||
		aws.ToString(in.ExpectedBucketOwner) != m.owner {
		m.t.Fatal("o teste alterou a criptografia ou enviou fora do namespace/proprietário isolado")
	}
	m.payload, _ = io.ReadAll(in.Body)
	if m.cancel != nil {
		m.cancel()
		return nil, ctx.Err()
	}
	if m.fail == "put" {
		return nil, errors.New("KMS: GenerateDataKey negado")
	}
	return &s3.PutObjectOutput{ServerSideEncryption: "aws:kms", VersionId: aws.String(m.versionID)}, nil
}

func (m *bucketCheckObjects) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.calls = append(m.calls, "get")
	if aws.ToString(in.Key) != m.key || aws.ToString(in.ExpectedBucketOwner) != m.owner {
		m.t.Fatal("a leitura apontou para outro objeto/proprietário")
	}
	if aws.ToString(in.VersionId) != m.versionID {
		m.t.Fatal("a leitura não fixou a versão do objeto criada pelo teste")
	}
	if m.fail == "get" {
		return nil, errors.New("KMS: Decrypt negado")
	}
	payload := m.payload
	if m.fail == "content" {
		payload = []byte("outro conteúdo")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(payload))}, nil
}

func (m *bucketCheckObjects) DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.t.Helper()
	m.calls = append(m.calls, "delete")
	if ctx.Err() != nil || aws.ToString(in.Key) != m.key || aws.ToString(in.ExpectedBucketOwner) != m.owner {
		m.t.Fatal("a limpeza herdou o cancelamento ou apontou para outro objeto/proprietário")
	}
	if _, ok := ctx.Deadline(); !ok {
		m.t.Fatal("a limpeza não possui limite de tempo")
	}
	if aws.ToString(in.VersionId) != m.versionID {
		m.t.Fatal("a limpeza de um bucket versionado não removeu a versão criada pelo teste")
	}
	return &s3.DeleteObjectOutput{}, m.deleteErr
}

func TestBucketDiagnosticChecksContentAndCleansItsOwnObject(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Cloud.S3Bucket = "synthetic-bucket"
	objects := &bucketCheckObjects{t: t, region: settings.Region, versionID: "only-probe-version"}
	result, err := checkBucket(t.Context(), settings, bucketCheckIdentity{}, objects)
	if err != nil || !result.Cleaned || result.Account != "123456789012" || result.Encryption != "aws:kms" ||
		!slices.Equal(objects.calls, []string{"head", "put", "get", "delete"}) {
		t.Fatal(result, objects.calls, err)
	}
}

func TestBucketDiagnosticAccessFailureStopsBeforeUploading(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Cloud.S3Bucket = "synthetic-bucket"
	objects := &bucketCheckObjects{t: t, region: settings.Region, fail: "head"}
	_, err := checkBucket(t.Context(), settings, bucketCheckIdentity{}, objects)
	if err == nil || !strings.Contains(err.Error(), "acesso") || !slices.Equal(objects.calls, []string{"head"}) {
		t.Fatal("falha de acesso enviou dados ou perdeu motivo", objects.calls, err)
	}
}
func TestBucketDiagnosticRejectsAnotherRegionBeforeUploading(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Cloud.S3Bucket = "synthetic-bucket"
	objects := &bucketCheckObjects{t: t, region: "us-west-2"}
	_, err := checkBucket(t.Context(), settings, bucketCheckIdentity{}, objects)
	if err == nil || !strings.Contains(err.Error(), "us-west-2") || !slices.Equal(objects.calls, []string{"head"}) {
		t.Fatal("região incompatível enviou dados ou não informa correção", objects.calls, err)
	}
}

func TestBucketDiagnosticCleansCanceledAndFailedProbes(t *testing.T) {
	for _, name := range []string{"put", "get", "content", "cancel", "delete"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			settings := config.Default()
			settings.Cloud.S3Bucket = "synthetic-bucket"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			objects := &bucketCheckObjects{t: t, region: settings.Region, fail: name}
			if name == "cancel" {
				objects.cancel = cancel
			}
			if name == "delete" {
				objects.deleteErr = errors.New("DeleteObject negado")
			}
			result, err := checkBucket(ctx, settings, bucketCheckIdentity{}, objects)
			if err == nil || objects.calls[len(objects.calls)-1] != "delete" {
				t.Fatal("uma falha foi aceita ou deixou o objeto sem tentativa de limpeza", result, objects.calls, err)
			}
			if name == "delete" {
				if result.Cleaned || !strings.Contains(err.Error(), result.Key) {
					t.Fatal("a falha de limpeza não informa o objeto pendente", result, err)
				}
			} else if !result.Cleaned {
				t.Fatal("limpeza bem sucedida não foi registrada", result)
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancelamento perdeu a causa original", err)
			}
		})
	}
}

func TestBucketDisabledRejectsProbeBeforeCredentialLoading(t *testing.T) {
	t.Parallel()
	settings := config.Default()
	settings.Profile = "profile-that-does-not-exist"
	settings.Cloud.S3Bucket = "retained-bucket"
	disabled := false
	settings.Cloud.UseS3 = &disabled
	_, err := TestBucket(t.Context(), settings)
	if !errors.Is(err, config.ErrTranscribeBucketRequired) {
		t.Fatal("bucket desabilitado tentou resolver credenciais", err)
	}
}
