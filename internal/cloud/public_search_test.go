package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type searchRoundTrip func(*http.Request) (*http.Response, error)

func (f searchRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func publicResponse(request *http.Request, payload, mediaType string) *http.Response {
	return &http.Response{
		StatusCode: 200, Header: http.Header{"Content-Type": []string{mediaType}},
		Body: io.NopCloser(strings.NewReader(payload)), Request: request,
	}
}

func TestPublicSearchFetchesOnlySearchFeedAndAddsDatedOfficialPTAX(t *testing.T) {
	var called []string
	client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
		called = append(called, request.URL.Hostname())
		switch request.URL.Hostname() {
		case "www.bing.com":
			if request.URL.Query().Get("q") != "cotação USD BRL hoje" || request.URL.Query().Get("format") != "rss" {
				t.Fatal("o buscador não recebeu a consulta original")
			}
			return publicResponse(request, `<rss><channel>
				<item><title>Fonte de referência</title><link>https://example.com/rates</link><description>Trecho pesquisado.</description></item>
				<item><title>Não abrir</title><link>file:///private</link><description>Ignorar.</description></item>
			</channel></rss>`, "text/xml"), nil
		case "olinda.bcb.gov.br":
			if request.URL.Query().Get("$top") != "1" || request.URL.Query().Get("$orderby") != "dataHoraCotacao desc" {
				t.Fatal("PTAX não pediu somente a publicação mais recente")
			}
			if strings.Contains(request.URL.RawQuery, "+") {
				t.Fatal("a consulta OData usou espaço de formulário em vez de URI")
			}
			return publicResponse(request, `{"value":[{"cotacaoCompra":5.2232,"cotacaoVenda":5.2238,"dataHoraCotacao":"2026-10-02 13:03:16.256632"}]}`, "application/json"), nil
		default:
			t.Fatal("um link de resultado foi aberto pelo aplicativo")
			return nil, errors.New("endereço inesperado")
		}
	})}
	found, err := publicSearch(t.Context(), client, "cotação USD BRL hoje", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if err != nil || len(called) != 2 || len(found.Sources) != 2 || found.Provider != "bing_search" ||
		!strings.Contains(found.Text, "5.2238 BRL (venda)") || !strings.Contains(found.Text, "2026-10-02 13:03:16.256632") ||
		!strings.Contains(found.Text, "não é uma cotação comercial ao vivo") {
		t.Fatalf("a busca perdeu fontes, valor, tipo ou data da referência: %+v, %v", found, err)
	}
}

func TestPublicSearchBoundsSnippetsAndDoesNotRepeatDuplicateResults(t *testing.T) {
	payload := `<rss><channel>` +
		`<item><title>` + strings.Repeat("á", 400) + `</title><link>https://example.com/first</link><description>` +
		strings.Repeat("é", 3000) + `</description></item>` +
		`<item><title>Duplicada</title><link>https://example.com/first</link><description>Trecho duplicado.</description></item>` +
		`<item><title>Longa demais</title><link>https://example.com/` + strings.Repeat("x", 4096) + `</link></item>` +
		`</channel></rss>`
	client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
		return publicResponse(request, payload, "text/xml"), nil
	})}
	found, err := publicSearch(t.Context(), client, "consulta", time.Now())
	if err != nil || len(found.Sources) != 1 || len([]rune(found.Text)) > 3000 ||
		strings.Contains(found.Text, "Trecho duplicado.") || strings.Contains(found.Text, "Longa demais") {
		t.Fatalf("a busca enviou trechos excessivos ou repetidos ao modelo: fontes=%d, %v", len(found.Sources), err)
	}
	if found.Sources[0].Title != strings.Repeat("á", 300)+"…" || !strings.Contains(found.Text, strings.Repeat("é", 2048)+"…") {
		t.Fatal("os limites da busca corromperam caracteres UTF-8")
	}
}

func TestPublicSearchKeepsOfficialRateWhenFeedFails(t *testing.T) {
	for _, feed := range []string{"<invalid", ""} {
		t.Run(feed, func(t *testing.T) {
			client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Hostname() == "olinda.bcb.gov.br" {
					return publicResponse(request, `{"value":[{"cotacaoCompra":5.1,"cotacaoVenda":5.2,"dataHoraCotacao":"2026-10-02 13:00:00"}]}`, "application/json"), nil
				}
				if feed == "" {
					return nil, errors.New("buscador indisponível")
				}
				return publicResponse(request, feed, "text/xml"), nil
			})}
			found, err := publicSearch(t.Context(), client, "USD BRL hoje", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
			if err != nil || found.Provider != "bcb_ptax" || len(found.Sources) != 1 ||
				!strings.Contains(found.Text, "5.2000 BRL") {
				t.Fatalf("um erro do buscador descartou a referência oficial disponível: %+v, %v", found, err)
			}
		})
	}
}

func TestPublicSearchKeepsSnippetsWhenPTAXIsUnavailable(t *testing.T) {
	client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "olinda.bcb.gov.br" {
			return publicResponse(request, `{"value":[]}`, "application/json"), nil
		}
		return publicResponse(request, `<rss><channel><item><title>Fonte</title><link>https://example.com</link><description>Sem valor.</description></item></channel></rss>`, "text/xml"), nil
	})}
	found, err := publicSearch(t.Context(), client, "USD BRL latest", time.Now())
	if err != nil || len(found.Sources) != 1 || !strings.Contains(found.Text, "Não invente a cotação") {
		t.Fatalf("a ausência de PTAX descartou as fontes ou inventou um valor: %+v, %v", found, err)
	}
}

func TestPublicSearchRejectsBlockedInvalidAndExcessiveResponses(t *testing.T) {
	for _, tc := range []struct {
		name, content, media string
		status               int
	}{
		{"bloqueio", "blocked", "text/xml", 403},
		{"página de bloqueio", "<html>blocked</html>", "text/html", 200},
		{"XML inválido", "invalid", "text/xml", 200},
		{"sem fontes", "<rss><channel/></rss>", "text/xml", 200},
		{"resposta excessiva", string(bytes.Repeat([]byte("a"), maxSearchResponse+1)), "text/xml", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
				response := publicResponse(request, tc.content, tc.media)
				response.StatusCode = tc.status
				return response, nil
			})}
			found, err := publicSearch(t.Context(), client, "consulta", time.Now())
			if err == nil || found.Text != "" || len(found.Sources) != 0 {
				t.Fatalf("uma resposta inválida virou informação pesquisada: %+v, %v", found, err)
			}
		})
	}
}

func TestPublicSearchHonorsCancellationAndDoesNotFetchPTAXForOtherCurrencies(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: searchRoundTrip(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}
	if _, err := publicSearch(ctx, client, "consulta", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("a busca ignorou o cancelamento: %v", err)
	}
	for _, query := range []string{"EUR BRL hoje", "USD BRL em 2020", "notícias do Brasil", "USD JPY today"} {
		if requestsCurrentUSDBRL(query, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("a busca acrescentou USD/BRL a outro pedido: %s", query)
		}
	}
}
