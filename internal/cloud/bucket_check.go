package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ai-talker/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type bucketIdentity interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

type bucketObjects interface {
	transcriptionObjects
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// BucketCheck describes the isolated S3 read/write/cleanup probe. It does not
// start a charged Transcribe job or assert that Transcribe has KMS permissions.
type BucketCheck struct {
	Bucket, Region, Account, Key string
	VersionID                    string
	Encryption                   string
	Cleaned                      bool
}

// TestBucket checks the selected profile, bucket access and region, then writes,
// reads and removes one synthetic object. Bucket encryption is never changed.
func TestBucket(ctx context.Context, settings config.Settings) (BucketCheck, error) {
	if strings.TrimSpace(settings.Region) == "" {
		return BucketCheck{}, errors.New("informe a região AWS")
	}
	if !settings.Cloud.S3Enabled() || strings.TrimSpace(settings.Cloud.S3Bucket) == "" {
		return BucketCheck{}, config.ErrTranscribeBucketRequired
	}
	if err := settings.Cloud.Validate(); err != nil {
		return BucketCheck{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cfg, closeHTTP, err := loadAWSConfig(ctx, settings, 15*time.Second)
	if err != nil {
		return BucketCheck{}, readableError(err, settings.Profile)
	}
	defer closeHTTP()
	return checkBucket(ctx, settings, sts.NewFromConfig(cfg), s3.NewFromConfig(cfg))
}

func checkBucket(ctx context.Context, settings config.Settings, identity bucketIdentity, objects bucketObjects) (result BucketCheck, err error) {
	result.Bucket, result.Region = settings.Cloud.S3Bucket, settings.Region
	caller, err := identity.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return result, fmt.Errorf("validar o perfil AWS: %w", readableError(err, settings.Profile))
	}
	if caller == nil || aws.ToString(caller.Account) == "" {
		return result, errors.New("a AWS não informou a conta do perfil selecionado")
	}
	result.Account = aws.ToString(caller.Account)
	// The caller's account is not necessarily the bucket owner's account.
	// Match the transcription client's access rules instead of inferring an owner.
	head, err := objects.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(result.Bucket)})
	if err != nil {
		return result, fmt.Errorf("verificar acesso ao bucket na região %s: %w", result.Region, err)
	}
	if head == nil || aws.ToString(head.BucketRegion) == "" {
		return result, errors.New("a AWS não confirmou a região do bucket; o teste foi interrompido antes de enviar dados")
	}
	if actual := aws.ToString(head.BucketRegion); actual != result.Region {
		return result, fmt.Errorf("o bucket está em %s; selecione essa região em Conexões → AWS", actual)
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, fmt.Errorf("preparar o objeto de diagnóstico: %w", err)
	}
	result.Key = "ai-talker/diagnostics/" + hex.EncodeToString(random[:]) + "/bucket-check.txt"
	payload := []byte("STT-widget: teste sintético de escrita, leitura e limpeza S3.\n")
	// An interrupted PUT can still have created the object. Cleanup therefore
	// covers ambiguous replies and cancellation, using only our generated key.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		input := &s3.DeleteObjectInput{
			Bucket: aws.String(result.Bucket), Key: aws.String(result.Key),
		}
		if result.VersionID != "" {
			input.VersionId = aws.String(result.VersionID)
		}
		_, cleanupErr := objects.DeleteObject(cleanupCtx, input)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("limpeza pendente: remova o objeto %s em %s: %w", result.Key, result.Bucket, cleanupErr))
		} else {
			result.Cleaned = true
		}
	}()
	put, err := objects.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(result.Bucket), Key: aws.String(result.Key),
		Body:        bytes.NewReader(payload),
		ContentType: aws.String("text/plain; charset=utf-8"),
	})
	if err != nil {
		return result, fmt.Errorf("enviar o objeto sintético (s3:PutObject e, com KMS, kms:GenerateDataKey): %w", err)
	}
	var versionID *string
	if put != nil {
		result.Encryption = string(put.ServerSideEncryption)
		result.VersionID = aws.ToString(put.VersionId)
		versionID = put.VersionId
	}
	get, err := objects.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(result.Bucket), Key: aws.String(result.Key),
		VersionId: versionID,
	})
	if err != nil {
		return result, fmt.Errorf("ler o objeto sintético (s3:GetObject e, com KMS, kms:Decrypt): %w", err)
	}
	if get == nil || get.Body == nil {
		return result, errors.New("a AWS não retornou o conteúdo do objeto de diagnóstico")
	}
	content, readErr := io.ReadAll(io.LimitReader(get.Body, int64(len(payload)+1)))
	closeErr := get.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return result, fmt.Errorf("conferir o conteúdo recebido: %w", err)
	}
	if !bytes.Equal(content, payload) {
		return result, errors.New("o conteúdo recebido não corresponde ao objeto enviado")
	}
	return result, nil
}
