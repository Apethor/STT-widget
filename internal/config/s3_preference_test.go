package config

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestS3PreferencePreservesLegacyAndExplicitlyDisabledBuckets(t *testing.T) {
	for _, tc := range []struct {
		name, encoded string
		enabled       bool
	}{
		{"legado com bucket", `{"s3_bucket":"retained-bucket"}`, true},
		{"legado sem bucket", `{}`, false},
		{"desabilitado", `{"s3_bucket":"retained-bucket","use_s3":false}`, false},
		{"habilitado", `{"s3_bucket":"retained-bucket","use_s3":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cloud CloudTranscription
			if err := json.Unmarshal([]byte(tc.encoded), &cloud); err != nil {
				t.Fatal(err)
			}
			settings := Default()
			settings.Cloud = cloud
			settings.Cloud.ModelID = TranscribeBatch
			if cloud.S3Enabled() != tc.enabled {
				t.Fatal("migração perdeu o uso explícito/legado do bucket", cloud)
			}
			err := settings.TranscriptionSetupError()
			if errors.Is(err, ErrTranscribeBucketRequired) == tc.enabled {
				t.Fatal("o pré-requisito ignorou o uso do bucket", err)
			}
			encoded, err := json.Marshal(cloud)
			if err != nil {
				t.Fatal(err)
			}
			var restored CloudTranscription
			if err := json.Unmarshal(encoded, &restored); err != nil || restored.S3Enabled() != tc.enabled ||
				restored.S3Bucket != cloud.S3Bucket {
				t.Fatal("roundtrip alterou a preferência ou apagou o nome", string(encoded), err)
			}
		})
	}
}
