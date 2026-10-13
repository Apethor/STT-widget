//go:build integration

package cloud

import (
	"os"
	"testing"

	"ai-talker/internal/config"
)

func TestBucketRealSyntheticDiagnostic(t *testing.T) {
	bucket := os.Getenv("AI_TALKER_TEST_BUCKET")
	if bucket == "" {
		t.Skip("defina AI_TALKER_TEST_BUCKET para o diagnóstico AWS explicitamente solicitado")
	}
	settings := config.Default()
	settings.Profile = os.Getenv("AI_TALKER_TEST_PROFILE")
	if region := os.Getenv("AI_TALKER_TEST_REGION"); region != "" {
		settings.Region = region
	}
	settings.Cloud.S3Bucket = bucket
	result, err := TestBucket(t.Context(), settings)
	t.Logf("bucket=%s region=%s account=%s key=%s encryption=%s cleaned=%v",
		result.Bucket, result.Region, result.Account, result.Key, result.Encryption, result.Cleaned)
	if err != nil || !result.Cleaned {
		t.Fatal("o diagnóstico sintético não confirmou envio, leitura e limpeza:", err)
	}
}
