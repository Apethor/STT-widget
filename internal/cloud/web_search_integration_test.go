//go:build awsintegration

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/processing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func TestNativeGroundingBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_WEB_SEARCH_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_WEB_SEARCH_AWS=1 para a busca sintética no Bedrock")
	}
	settings := config.Default()
	settings.Profile = config.DefaultProfile()
	if profile := os.Getenv("AI_TALKER_TEST_PROFILE"); profile != "" {
		settings.Profile = profile
	}
	cfg, closeConfig, err := loadAWSConfig(t.Context(), settings, 5*time.Minute)
	if err != nil {
		t.Fatal(readableError(err, settings.Profile))
	}
	defer closeConfig()
	client := bedrockruntime.NewFromConfig(cfg)
	var output *bedrockruntime.ConverseOutput
	observed := converseFunc(func(ctx context.Context, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		var err error
		output, err = client.Converse(ctx, input)
		t.Logf("modelo auxiliar=%s", aws.ToString(input.ModelId))
		return output, err
	})
	result, err := groundedSearch(t.Context(), settings.Profile, observed,
		"Pesquise na web os comunicados oficiais mais recentes do Banco Central do Brasil sobre a taxa Selic. Informe a data do comunicado e cite a página oficial. Use a pesquisa para consultar as informações atuais.")
	if err != nil {
		// This query and response concern public information only. Keep these
		// diagnostics inside the explicitly opted-in integration test.
		if output != nil {
			content, marshalErr := json.Marshal(output.Output)
			if marshalErr == nil {
				t.Logf("resposta pública de grounding: %s", content)
			}
		}
		t.Fatal(err)
	}
	t.Logf("fontes=%d texto=%q", len(result.Sources), result.Text)
}

// Only an explicit opt-in sends this synthetic public-information query.
func TestWebSearchBedrockIntegration(t *testing.T) {
	if os.Getenv("AI_TALKER_TEST_WEB_SEARCH_AWS") != "1" {
		t.Skip("ative AI_TALKER_TEST_WEB_SEARCH_AWS=1 para a busca sintética no Bedrock")
	}
	settings := config.Default()
	settings.Profile = config.DefaultProfile()
	if profile := os.Getenv("AI_TALKER_TEST_PROFILE"); profile != "" {
		settings.Profile = profile
	}
	if region := os.Getenv("AI_TALKER_TEST_REGION"); region != "" {
		settings.Region = region
	}
	options := processing.Default()
	options.Mode = processing.ModeFree
	request := processing.Request{
		Text:    "Faça uma busca web pela cotação mais recente de um dólar em reais, USD/BRL. Informe a data e a fonte, deixando claro se é cotação comercial ou PTAX. Use a ferramenta web_search; não responda com informação de memória.",
		Options: options,
	}
	cfg, closeConfig, err := loadAWSConfig(t.Context(), settings, 5*time.Minute)
	if err != nil {
		t.Fatal(readableError(err, settings.Profile))
	}
	defer closeConfig()
	expected, err := latestPTAX(t.Context(), cfg.HTTPClient, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := Process(t.Context(), settings, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) == 0 || result.Usage["webSearch"] == nil ||
		(!strings.Contains(strings.ToLower(result.Text), "dólar") && !strings.Contains(result.Text, "USD")) {
		t.Fatalf("a ferramenta não trouxe fontes e a informação pública esperada: %+v", result)
	}
	// An end-to-end pass must include the verified published value, not merely
	// a refusal with links. The reference is public and supplied by the BCB.
	foundValue := false
	for _, rate := range []any{expected.Facts["buy"], expected.Facts["sell"]} {
		value := fmt.Sprintf("%.4f", rate)
		foundValue = foundValue || strings.Contains(result.Text, value) ||
			strings.Contains(result.Text, strings.ReplaceAll(value, ".", ","))
	}
	if !foundValue || !strings.Contains(strings.ToLower(result.Text), "ptax") {
		t.Fatalf("a pesquisa não entregou o valor oficial consultado: %v; resposta=%q", expected.Facts, result.Text)
	}
	t.Logf("modelo=%s fontes=%d duração=%.2fs uso=%v texto=%q", result.ModelID, len(result.Sources), result.ElapsedSeconds, result.Usage, result.Text)
}
