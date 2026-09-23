package harvest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type legacyConverterFunc func(context.Context, string, string, []byte) (string, error)

func (convert legacyConverterFunc) Convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	return convert(ctx, kind, source, body)
}

func TestLegacyNegativeCacheAnnotatesRetryAndExpiresByFailureKind(t *testing.T) {
	cache := newNegativeCache(120*time.Second, 15*time.Second)
	permanent := Result{Source: "https://fixture.example.test/permanent", Error: "forbidden", HTTPStatus: 403}
	cache.put("permanent", permanent)
	got, ok := cache.get("permanent")
	if !ok || !strings.Contains(got.Error, "recently failed; cached") || !strings.Contains(got.Error, "retry after") {
		t.Fatalf("negative-cache receipt=%#v ok=%v", got, ok)
	}
	if permanent.Error != "forbidden" {
		t.Fatalf("negative cache mutated stored/caller result: %#v", permanent)
	}

	for _, test := range []struct {
		name   string
		result Result
		age    time.Duration
		want   bool
	}{
		{"timeout expires on transient TTL", Result{Error: "timeout", ErrorKind: "timeout"}, 16 * time.Second, false},
		{"429 expires on transient TTL", Result{Error: "rate limited", HTTPStatus: 429}, 16 * time.Second, false},
		{"403 keeps full TTL", Result{Error: "forbidden", HTTPStatus: 403}, 16 * time.Second, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := test.name
			cache.put(key, test.result)
			cache.mu.Lock()
			entry := cache.entries[key]
			entry.at = time.Now().Add(-test.age)
			cache.entries[key] = entry
			cache.mu.Unlock()
			_, ok := cache.get(key)
			if ok != test.want {
				t.Fatalf("cache presence=%v, want %v", ok, test.want)
			}
		})
	}
}

func TestLegacyConcurrentFailuresShareOneInFlightFetch(t *testing.T) {
	var calls int
	var mu sync.Mutex
	release := make(chan struct{})
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return nil, fmt.Errorf("fixture connection failure")
	})
	client := func() *http.Client { return &http.Client{Transport: transport} }
	h := mustNew(
		t,
		Options{
			ContactEmail: "test@example.org",
			CacheDir:     t.TempDir(),
			Client:       client(),
			Chrome:       client(),
			Jina:         client(),
			OA:           client(),
			Converter: legacyConverterFunc(
				func(context.Context, string, string, []byte) (string, error) { return "", nil },
			),
			BrowserRung: browserOff(),
		},
	)
	start := make(chan struct{})
	results := make(chan Result, 2)
	for range 2 {
		go func() {
			<-start
			results <- h.Fetch(context.Background(), "https://race.example.test/source")
		}()
	}
	close(start)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		started := calls
		mu.Unlock()
		if started > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	first, second := <-results, <-results
	if first.Error == "" || second.Error != first.Error {
		t.Fatalf("shared failures=(%#v,%#v)", first, second)
	}
	wantRungs := []string{"direct", "chrome-impersonation", "jina", "defuddle", "wayback"}
	if fmt.Sprint(first.Rungs) != fmt.Sprint(wantRungs) {
		t.Fatalf("terminal rescue trace=%#v, want %#v", first.Rungs, wantRungs)
	}
	// One logical fetch walks direct, Chrome, Jina, defuddle, and Wayback. Its
	// twin must share that whole walk instead of doubling the transport count.
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 5 {
		t.Fatalf("transport calls=%d, want one five-rung walk", gotCalls)
	}
}

func TestLegacyDOINegativeCacheFormsShareOneKey(t *testing.T) {
	want := canonicalNegativeKey("fetch", "10.1234/example")
	for _, form := range []string{"doi:10.1234/example", "https://doi.org/10.1234/example"} {
		if got := canonicalNegativeKey("fetch", form); got != want {
			t.Fatalf("canonical key for %q=%q, want %q", form, got, want)
		}
	}
}

func TestLegacyPDFErrorBodiesNeverBecomeConvertedSuccess(t *testing.T) {
	wall := `<html><head><title>Article unavailable</title></head><body>` + strings.Repeat(
		"the requested document is unavailable ",
		80,
	) + `</body></html>`
	wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "text/html", wall), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: wallTransport},
		Chrome:   &http.Client{Transport: wallTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(func(_ context.Context, kind, _ string, body []byte) (string, error) {
			return "converted " + kind + "\n" + string(body), nil
		}),
	})
	result := h.Fetch(context.Background(), "https://publisher.example.test/paper.pdf")
	if result.Error == "" {
		t.Fatalf("publisher 403 HTML became success: %#v", result)
	}
	var artifacts []string
	_ = filepath.Walk(h.options.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			// stats.jsonl is the fetch-outcome SCOREBOARD (telemetry), not a
			// content artifact — a failing fetch may still record its outcome.
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if len(artifacts) != 0 {
		t.Fatalf("publisher error body entered cache: %#v", artifacts)
	}
}

func TestLegacyEmptyPDFConversionKeepsOCRRecoveryHint(t *testing.T) {
	pdfTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "application/pdf", "%PDF-1.4 fixture"), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: pdfTransport},
		Chrome:   &http.Client{Transport: pdfTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
			return "", nil
		}),
	})
	result := h.Fetch(context.Background(), "https://scanned.example.test/paper.pdf")
	// SearchAvailable defaults false here, so the recovery hint names findWorks
	// rather than the unavailable `webSearch` tool — see SearchHint.
	if result.Error == "" || !strings.Contains(result.Error, "convert.pdfOcr=true in harvester.config.json") ||
		!strings.Contains(strings.ToLower(result.Error), "alternative copy") {
		t.Fatalf("empty PDF conversion receipt=%#v", result)
	}
}

func TestLegacyPlainTextPassesThroughVerbatimAndCaches(t *testing.T) {
	body := "CHAPTER I\n\n" + strings.Repeat("Napoleon and the war and peace of nations. ", 4000)
	var calls int
	plainTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return response(request, http.StatusOK, "text/plain; charset=utf-8", body), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: plainTransport},
		Chrome:   &http.Client{Transport: plainTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
			return "", fmt.Errorf("plain text must not enter the document converter")
		}),
	})
	url := "https://public.example.test/book.txt"
	first := h.Fetch(context.Background(), url)
	if first.Error != "" || first.Method != "plain-text" ||
		!strings.HasPrefix(first.Content, "CHAPTER I\n\nNapoleon") ||
		!strings.Contains(first.Content, "content truncated") {
		t.Fatalf("plain-text fetch=%#v", first)
	}
	cachedBody, err := os.ReadFile(first.Path)
	if err != nil || !strings.Contains(string(cachedBody), body) {
		t.Fatalf("plain-text cache did not preserve the complete body: path=%q err=%v", first.Path, err)
	}
	second := h.Fetch(context.Background(), url)
	if second.Error != "" || second.CacheStatus != "hit" || second.Content != first.Content || calls != 1 {
		t.Fatalf("cached plain-text fetch=%#v calls=%d", second, calls)
	}

	localPath := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(localPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	local := mustNew(
		t,
		Options{CacheDir: t.TempDir(), LocalRoots: []string{filepath.Dir(localPath)}, Converter: h.options.Converter},
	).Fetch(context.Background(), localPath)
	localBody, localReadErr := os.ReadFile(local.Path)
	if local.Error != "" || !strings.HasPrefix(local.Content, "CHAPTER I\n\nNapoleon") || localReadErr != nil ||
		!strings.Contains(string(localBody), body) {
		t.Fatalf("local plain-text fetch=%#v", local)
	}
}

func TestLegacyMisservedHTMLAsTextPlainStillUsesHTMLConverter(t *testing.T) {
	body := "<html><body>" + strings.Repeat("article prose ", 100) + "</body></html>"
	plainTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain", body), nil
	})
	var kinds []string
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: plainTransport},
		Chrome:   &http.Client{Transport: plainTransport},
		Converter: legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
			kinds = append(kinds, kind)
			return string(raw), nil
		}),
	})
	result := h.Fetch(context.Background(), "https://public.example.test/misserved")
	if result.Error != "" || result.Kind != "html" || len(kinds) != 1 || kinds[0] != "html" {
		t.Fatalf("misserved HTML fetch=%#v converter kinds=%#v", result, kinds)
	}
}

func TestLegacyDOISuccessCachesUnderCanonicalIdentifierBeforeProviderResolution(t *testing.T) {
	var providerCalls atomic.Int64
	oaTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		if strings.Contains(request.URL.Host, "unpaywall") {
			return jsonResponse(
				request,
				`{"is_oa":true,"oa_status":"green","best_oa_location":{"url_for_pdf":"https://public.example.test/paper.pdf","version":"publishedVersion"}}`,
			), nil
		}
		return jsonResponse(request, `{}`), nil
	})
	var documentCalls atomic.Int64
	documentTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		documentCalls.Add(1)
		return response(request, http.StatusOK, "application/pdf", "%PDF-1.7 fixture"), nil
	})
	h := mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     t.TempDir(),
		Client:       &http.Client{Transport: documentTransport},
		Chrome:       &http.Client{Transport: documentTransport},
		Jina:         &http.Client{Transport: documentTransport},
		OA:           &http.Client{Transport: oaTransport},
		Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
			return "# Open paper\n\ncomplete scholarly fixture", nil
		}),
	})
	first := h.Fetch(context.Background(), "10.1234/cache-parity")
	if first.Error != "" || first.Content == "" {
		t.Fatalf("first DOI fetch=%#v", first)
	}
	providersAfterFirst := providerCalls.Load()
	documentsAfterFirst := documentCalls.Load()
	if providersAfterFirst == 0 || documentsAfterFirst != 1 {
		t.Fatalf("first DOI calls providers=%d documents=%d", providersAfterFirst, documentsAfterFirst)
	}
	second := h.Fetch(context.Background(), "https://doi.org/10.1234/cache-parity")
	if second.Error != "" || second.CacheStatus != "hit" {
		t.Fatalf("canonical DOI cache fetch=%#v", second)
	}
	if providerCalls.Load() != providersAfterFirst || documentCalls.Load() != documentsAfterFirst {
		t.Fatalf(
			"canonical DOI cache re-resolved providers=%d->%d documents=%d->%d",
			providersAfterFirst,
			providerCalls.Load(),
			documentsAfterFirst,
			documentCalls.Load(),
		)
	}
}

func TestLegacyPDFAddressMustContainPDFBytesLocallyAndRemotely(t *testing.T) {
	wall := "<html><head><title>Subscriber access</title></head><body>" + strings.Repeat(
		"paywall login required ",
		100,
	) + "</body></html>"
	t.Run("local renamed HTML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "renamed.pdf")
		if err := os.WriteFile(path, []byte(wall), 0o600); err != nil {
			t.Fatal(err)
		}
		var converts atomic.Int64
		h := mustNew(t, Options{
			CacheDir:   t.TempDir(),
			LocalRoots: []string{filepath.Dir(path)},
			Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
				converts.Add(1)
				return "wrong success", nil
			}),
		})
		result := h.Fetch(context.Background(), path)
		if result.Error == "" || !strings.Contains(result.Error, "has a .pdf extension but is not a PDF file") ||
			converts.Load() != 0 {
			t.Fatalf("renamed local PDF=%#v converts=%d", result, converts.Load())
		}
	})

	t.Run("remote paywall HTML", func(t *testing.T) {
		wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request, http.StatusOK, "text/html", wall), nil
		})
		missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request, http.StatusNotFound, "application/json", `{}`), nil
		})
		var converts atomic.Int64
		h := mustNew(t, Options{
			CacheDir: t.TempDir(),
			Client:   &http.Client{Transport: wallTransport},
			Chrome:   &http.Client{Transport: wallTransport},
			Jina:     &http.Client{Transport: missingTransport},
			OA:       &http.Client{Transport: missingTransport},
			Converter: legacyConverterFunc(func(_ context.Context, _, _ string, raw []byte) (string, error) {
				converts.Add(1)
				return string(raw), nil
			}),
		})
		result := h.Fetch(context.Background(), "https://publisher.example.test/paper.pdf")
		if result.Error == "" || !strings.Contains(result.Error, "has a .pdf address but did not return a PDF") ||
			converts.Load() != 0 {
			t.Fatalf("remote PDF paywall=%#v converts=%d", result, converts.Load())
		}
		if artifacts, _ := filepath.Glob(filepath.Join(h.options.CacheDir, "pdf", "*.md")); len(artifacts) != 0 {
			t.Fatalf("remote PDF paywall entered PDF cache: %#v", artifacts)
		}
	})
}

func TestLegacyPaywalledDOIUsesWaybackThenReturnsCompleteLegalSourceReceipt(t *testing.T) {
	t.Run("no snapshot", func(t *testing.T) {
		oaTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return jsonResponse(request, `{}`), nil
		})
		h := mustNew(
			t,
			Options{
				CacheDir:        t.TempDir(),
				OA:              &http.Client{Transport: oaTransport},
				SearchAvailable: true,
				Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
					return "", nil
				}),
			},
		)
		result := h.Fetch(context.Background(), "10.1234/paywalled")
		for _, want := range []string{"likely paywalled", "Unpaywall", "OpenAlex", "Semantic Scholar", "Europe PMC", "CORE", "DOAJ", "Wayback Machine", "`webSearch`"} {
			if !strings.Contains(result.Error, want) {
				t.Fatalf("paywall receipt missing %q: %#v", want, result)
			}
		}
		if len(result.Rungs) != 1 || result.Rungs[0] != "wayback" {
			t.Fatalf("paywall rungs=%#v, want wayback", result.Rungs)
		}
	})

	t.Run("snapshot success is canonical-cache hit", func(t *testing.T) {
		var providerCalls atomic.Int64
		oaTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			providerCalls.Add(1)
			if request.URL.Host == "archive.org" {
				return jsonResponse(
					request,
					`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20230601123456"}}}`,
				), nil
			}
			return jsonResponse(request, `{}`), nil
		})
		page := "<html><body><h1>Archived paper</h1>" + strings.Repeat(
			"complete scientific text ",
			100,
		) + "</body></html>"
		pageTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request, http.StatusOK, "text/html", page), nil
		})
		h := mustNew(t, Options{
			CacheDir: t.TempDir(),
			Client:   &http.Client{Transport: pageTransport},
			Chrome:   &http.Client{Transport: pageTransport},
			Jina:     &http.Client{Transport: pageTransport},
			OA:       &http.Client{Transport: oaTransport},
			Converter: legacyConverterFunc(func(_ context.Context, _, _ string, raw []byte) (string, error) {
				return string(raw), nil
			}),
		})
		first := h.Fetch(context.Background(), "doi:10.1234/archived")
		if first.Error != "" || first.Method != "mirror:wayback" || first.Source != "doi:10.1234/archived" ||
			first.Content == "" {
			t.Fatalf("Wayback DOI fetch=%#v", first)
		}
		firstCalls := providerCalls.Load()
		second := h.Fetch(context.Background(), "https://doi.org/10.1234/archived")
		if second.Error != "" || second.CacheStatus != "hit" || providerCalls.Load() != firstCalls {
			t.Fatalf("Wayback DOI cache fetch=%#v providers=%d->%d", second, firstCalls, providerCalls.Load())
		}
	})
}
