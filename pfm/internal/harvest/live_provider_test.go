package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/harvestpy"
)

const (
	liveProviderDOI   = "10.1371/journal.pone.0033693"
	liveProviderPMCID = "PMC3312333"
	liveProviderMD5   = "9de4a86150a39b54d3e01f98678468bf"
)

// These tests are deliberately opt-in. A default run skips them; selecting a
// provider requires the caller to inspect its status line and does not turn an
// unavailable mirror into a download pass.
func TestLiveProviderDOIMirror(t *testing.T) {
	runLiveProvider(t, "doi-mirror", liveMirrorURL("doi-mirror"), func(ctx context.Context, h *Harvester) Result {
		return h.fetchDOIMirror(ctx, liveRequestedDOI(), FetchOptions{})
	})
}

func TestLiveProviderDOIViewer(t *testing.T) {
	runLiveProvider(t, "doi-viewer", liveMirrorURL("doi-viewer"), func(ctx context.Context, h *Harvester) Result {
		return h.fetchDOIViewerDOI(ctx, liveRequestedDOI(), FetchOptions{})
	})
}

func TestLiveProviderAnna(t *testing.T) {
	runLiveProvider(t, "ipfs-catalog", liveMirrorURL("ipfs-catalog"), func(ctx context.Context, h *Harvester) Result {
		return h.fetchIPFSCatalogMD5(ctx, liveRequestedDOI(), liveProviderMD5, FetchOptions{})
	})
}

func TestLiveProviderMD5Catalog(t *testing.T) {
	runLiveProvider(t, "md5-catalog", liveMirrorURL("md5-catalog"), func(ctx context.Context, h *Harvester) Result {
		return h.fetchMD5CatalogDOI(ctx, liveRequestedDOI(), FetchOptions{})
	})
}

func TestLiveProviderScholar(t *testing.T) {
	runLiveProvider(t, "scholar", "https://scholar.google.com", func(ctx context.Context, h *Harvester) Result {
		return h.fetchScholarDOI(ctx, liveRequestedDOI(), FetchOptions{})
	})
}

func TestLiveProviderUnpaywall(t *testing.T) {
	runLiveProvider(t, "unpaywall", "https://api.unpaywall.org", func(ctx context.Context, h *Harvester) Result {
		candidates, err := h.resolver().unpaywall(ctx, h.oa, liveRequestedDOI())
		if err != nil {
			return Result{Source: liveRequestedDOI(), Error: "unavailable", ErrorKind: "unavailable"}
		}
		return fetchFirstLiveCandidate(ctx, h, candidates)
	})
}

func TestLiveProviderPMC(t *testing.T) {
	runLiveProvider(t, "pmc", "https://pmc.ncbi.nlm.nih.gov", func(ctx context.Context, h *Harvester) Result {
		return h.fetchKnownID(ctx, liveProviderPMCID, IdentifierPMCID, FetchOptions{})
	})
}

// TestLiveFetchRequestedDOI exercises the public seam with every configured
// scholarly provider. It is opt-in because it performs real provider requests
// and starts the pinned conversion worker.
func TestLiveFetchRequestedDOI(t *testing.T) {
	if !liveProviderSelected("requested-doi") {
		t.Skipf(
			"provider=requested-doi status=opt-in-required kind= chars=0 valid=false complete=false receipt_safe=false",
		)
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	python, script := liveWorkerConfig(t, "requested-doi")
	worker := &liveWorkerConverter{
		worker: harvestpy.NewConverter(harvestpy.Runtime{Python: python, Script: script}),
		dir:    t.TempDir(),
	}
	t.Cleanup(func() { _ = worker.worker.Close() })
	cacheDir := t.TempDir()
	h, err := New(Options{
		CacheDir:         cacheDir,
		Converter:        worker,
		DOIMirrorURL:     liveMirrorURL("doi-mirror"),
		IPFSCatalogURL:   liveMirrorURL("ipfs-catalog"),
		DOIViewerURL:     liveMirrorURL("doi-viewer"),
		MD5CatalogURL:    liveMirrorURL("md5-catalog"),
		GoogleScholarURL: "https://scholar.google.com",
		ContactEmail:     strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")),
	})
	if err != nil {
		t.Fatalf(
			"provider=requested-doi status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
		)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result := h.FetchPublic(ctx, liveRequestedDOI(), FetchOptions{})
	if result.Error != "" {
		t.Errorf(
			"provider=requested-doi status=unavailable kind=%s http_status=%d chars=0 valid=false complete=false receipt_safe=true",
			safeLiveErrorKind(result.ErrorKind),
			result.HTTPStatus,
		)
		return
	}
	complete := regularFileNonEmpty(result.Path)
	receiptSafe := liveReceiptSafe(result, strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")))
	artifactMetadataSafe := liveArtifactMetadataSafe(result, strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")))
	valid := result.Path != "" && complete && receiptSafe && artifactMetadataSafe && result.Error == ""
	if valid {
		valid = assertLiveExpectedText(t, "requested-doi", result)
		retainLiveOutput(t, "requested-doi", result)
	}
	status := "pass"
	if !valid {
		status = "invalid"
	}
	t.Logf(
		"provider=requested-doi status=%s kind=%s chars=%d valid=%t complete=%t receipt_safe=%t",
		status,
		safeLiveKind(result.Kind),
		result.Chars,
		valid,
		complete,
		receiptSafe,
	)
	if !valid {
		t.Errorf("provider=requested-doi did not produce a valid public artifact")
	}
}

func fetchFirstLiveCandidate(ctx context.Context, h *Harvester, candidates []Candidate) Result {
	if len(candidates) == 0 {
		return Result{Source: liveRequestedDOI(), Error: "unavailable", ErrorKind: "missing"}
	}
	var last Result
	for _, candidate := range candidates {
		last = h.fetchURLWithPolicy(ctx, candidate.URL, FetchOptions{}, false)
		if last.Error == "" {
			return last
		}
	}
	return last
}

func runLiveProvider(t *testing.T, name, baseURL string, fetch func(context.Context, *Harvester) Result) {
	t.Helper()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	if !liveProviderSelected(name) {
		t.Skipf("provider=%s status=opt-in-required kind= chars=0 valid=false complete=false receipt_safe=false", name)
	}
	if env, mirror := liveMirrorEnv[name]; mirror && baseURL == "" {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false — set %s to the mirror's base URL",
			name,
			env,
		)
	}
	python, script := strings.TrimSpace(
		os.Getenv("HARVESTER_LIVE_PYTHON"),
	), strings.TrimSpace(
		os.Getenv("HARVESTER_LIVE_SCRIPT"),
	)
	if python == "" || script == "" {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}

	cacheDir := t.TempDir()
	worker := &liveWorkerConverter{
		worker: harvestpy.NewConverter(harvestpy.Runtime{Python: python, Script: script}),
		dir:    t.TempDir(),
	}
	options := Options{
		CacheDir:         cacheDir,
		Converter:        worker,
		DOIMirrorURL:     "",
		IPFSCatalogURL:   "",
		DOIViewerURL:     "",
		MD5CatalogURL:    "",
		GoogleScholarURL: "",
		ContactEmail:     strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")),
	}
	switch name {
	case "doi-mirror":
		options.DOIMirrorURL = baseURL
	case "doi-viewer":
		options.DOIViewerURL = baseURL
	case "ipfs-catalog":
		options.IPFSCatalogURL = baseURL
	case "md5-catalog":
		options.MD5CatalogURL = baseURL
	case "scholar":
		options.GoogleScholarURL = baseURL
	}
	h, err := New(options)
	if err != nil {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	t.Cleanup(func() { _ = worker.worker.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result := fetch(ctx, h)
	if result.Error != "" {
		writeLiveDiagnostic(name, result)
		t.Errorf(
			"provider=%s status=unavailable kind=%s http_status=%d chars=0 valid=false complete=false receipt_safe=true",
			name,
			safeLiveErrorKind(result.ErrorKind),
			result.HTTPStatus,
		)
		return
	}
	public := h.PublicResult(liveProviderSource(name), result, false)
	complete := false
	if public.Path != "" {
		if info, statErr := os.Stat(public.Path); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			complete = true
		}
	}
	receiptSafe := liveReceiptSafe(public, strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")))
	artifactMetadataSafe := liveArtifactMetadataSafe(public, strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT")))
	valid := public.Error == "" && public.Path != "" && complete && receiptSafe && artifactMetadataSafe
	if valid {
		valid = assertLiveExpectedText(t, name, public)
		retainLiveOutput(t, name, public)
	}
	status := "pass"
	if !valid {
		status = "invalid"
	}
	t.Logf(
		"provider=%s status=%s kind=%s chars=%d valid=%t complete=%t receipt_safe=%t",
		name,
		status,
		safeLiveKind(public.Kind),
		public.Chars,
		valid,
		complete,
		receiptSafe,
	)
	if !valid {
		t.Errorf("provider=%s did not produce a valid public artifact", name)
	}
}

// liveMirrorEnv names the variable carrying each mirror mirror's base
// URL. Mirror hosts are private configuration — they live in the operator's
// harvester config, never in tracked source — so a live run supplies them.
var liveMirrorEnv = map[string]string{
	"doi-mirror":   "HARVESTER_LIVE_DOI_MIRROR_URL",
	"doi-viewer":   "HARVESTER_LIVE_DOI_VIEWER_URL",
	"ipfs-catalog": "HARVESTER_LIVE_IPFS_CATALOG_URL",
	"md5-catalog":  "HARVESTER_LIVE_MD5_CATALOG_URL",
}

func liveMirrorURL(name string) string {
	return strings.TrimSpace(os.Getenv(liveMirrorEnv[name]))
}

// liveProviderSecrets lists every acquisition host and the contact address a
// public receipt must never carry: the configured mirror hosts plus the
// public providers' API hosts.
func liveProviderSecrets(contact string) []string {
	secrets := []string{"scholar.google.com", "api.unpaywall.org", "pmc.ncbi.nlm.nih.gov", contact}
	for _, name := range []string{"doi-mirror", "doi-viewer", "ipfs-catalog", "md5-catalog"} {
		raw := liveMirrorURL(name)
		if raw == "" {
			continue
		}
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			secrets = append(secrets, parsed.Host)
		} else {
			secrets = append(secrets, raw)
		}
	}
	return secrets
}

func liveProviderSelected(name string) bool {
	for _, value := range strings.Split(strings.ToLower(os.Getenv("HARVESTER_LIVE_PROVIDERS")), ",") {
		if strings.TrimSpace(value) == "all" || strings.TrimSpace(value) == name {
			return true
		}
	}
	return false
}

func liveProviderSource(name string) string {
	if name == "pmc" {
		return liveProviderPMCID
	}
	return liveRequestedDOI()
}

func liveRequestedDOI() string {
	if value := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_DOI")); value != "" {
		return value
	}
	return liveProviderDOI
}

func liveWorkerConfig(t *testing.T, name string) (string, string) {
	t.Helper()
	python := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_PYTHON"))
	script := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_SCRIPT"))
	if python == "" || script == "" {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf(
			"provider=%s status=unavailable kind=configuration chars=0 valid=false complete=false receipt_safe=false",
			name,
		)
	}
	return python, script
}

func regularFileNonEmpty(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func safeLiveKind(kind string) string {
	low := strings.ToLower(strings.TrimSpace(kind))
	switch low {
	case "pdf", "html", "txt", "epub", "docx", "xlsx", "pptx", "csv":
		return low
	default:
		return ""
	}
}

func safeLiveErrorKind(kind string) string {
	low := strings.ToLower(strings.TrimSpace(kind))
	switch low {
	case "challenge",
		"connect",
		"conversion",
		"dns",
		"disabled",
		"http",
		"integrity",
		"invalid",
		"malformed",
		"missing",
		"timeout",
		"too_large",
		"unavailable",
		"wrong_kind":
		return low
	default:
		return "failed"
	}
}

func writeLiveDiagnostic(name string, result Result) {
	path := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_LOG"))
	if path == "" {
		return
	}
	contact := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_CONTACT"))
	detail := result.Error
	if contact != "" {
		detail = strings.ReplaceAll(detail, contact, "[contact-redacted]")
	}
	line := fmt.Sprintf(
		"provider=%s error_kind=%s http_status=%d challenge=%t error=%s\n",
		name,
		safeLiveErrorKind(result.ErrorKind),
		result.HTTPStatus,
		result.Challenge,
		detail,
	)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = file.Chmod(0o600)
	_, _ = file.WriteString(line)
}

func liveReceiptSafe(result Result, contact string) bool {
	// Content is article text, not receipt metadata; it may contain legitimate
	// bibliography URLs such as PMC citations. Keep the receipt assertion on
	// fields that adapters actually expose as acquisition metadata.
	receiptResult := result
	receiptResult.Content = ""
	receipt, err := json.Marshal(receiptResult)
	if err != nil {
		return false
	}
	text := string(receipt)
	for _, secret := range liveProviderSecrets(contact) {
		if secret != "" && strings.Contains(text, secret) {
			return false
		}
	}
	// Article text may legitimately cite a repository (especially PMC). The
	// privacy assertion belongs to generated receipt/source metadata; content
	// validation is handled separately by HARVESTER_LIVE_EXPECT_TEXT.
	return result.Error == ""
}

func liveArtifactMetadataSafe(result Result, contact string) bool {
	content := result.Content
	if content == "" && result.Path != "" {
		body, err := os.ReadFile(result.Path)
		if err != nil {
			return false
		}
		content = string(body)
	}
	// Only the generated metadata prefix is private. Article citations in the
	// body may legitimately mention a repository such as PMC.
	if separator := strings.Index(content, "\n---\n"); separator >= 0 {
		content = content[:separator]
	} else if len(content) > 4096 {
		content = content[:4096]
	}
	for _, secret := range liveProviderSecrets(contact) {
		if secret != "" && strings.Contains(strings.ToLower(content), strings.ToLower(secret)) {
			return false
		}
	}
	return true
}

func assertLiveExpectedText(t *testing.T, name string, result Result) bool {
	t.Helper()
	marker := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_EXPECT_TEXT"))
	section := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_EXPECT_SECTION"))
	if marker == "" && section == "" {
		return true
	}
	var content string
	if result.Path != "" {
		body, err := os.ReadFile(result.Path)
		if err != nil {
			t.Errorf(
				"provider=%s status=invalid kind=artifact_read chars=0 valid=false complete=false receipt_safe=true",
				name,
			)
			return false
		}
		content = string(body)
	} else {
		content = result.Content
	}
	valid := true
	if marker != "" && !containsLiveMarker(content, marker) {
		t.Errorf(
			"provider=%s status=invalid kind=unexpected_text chars=%d valid=false complete=true receipt_safe=true",
			name,
			result.Chars,
		)
		valid = false
	}
	if section != "" && !containsLiveMarker(content, section) {
		t.Errorf(
			"provider=%s status=invalid kind=unexpected_section chars=%d valid=false complete=true receipt_safe=true",
			name,
			result.Chars,
		)
		valid = false
	}
	return valid
}

func containsLiveMarker(content, marker string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.NewReplacer("’", "'", "‘", "'", "′", "'").Replace(value))
	}
	return strings.Contains(normalize(content), normalize(marker))
}

func retainLiveOutput(t *testing.T, name string, result Result) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("HARVESTER_LIVE_OUTPUT"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Errorf(
			"provider=%s status=invalid kind=output_write chars=0 valid=false complete=true receipt_safe=true",
			name,
		)
		return
	}
	receipt, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Errorf(
			"provider=%s status=invalid kind=receipt_write chars=0 valid=false complete=true receipt_safe=true",
			name,
		)
		return
	}
	receiptPath := filepath.Join(dir, name+"-receipt.json")
	if err := os.WriteFile(receiptPath, append(receipt, '\n'), 0o600); err != nil {
		t.Errorf(
			"provider=%s status=invalid kind=receipt_write chars=0 valid=false complete=true receipt_safe=true",
			name,
		)
		return
	}
	if result.Path == "" {
		return
	}
	body, err := os.ReadFile(result.Path)
	if err != nil {
		t.Errorf(
			"provider=%s status=invalid kind=artifact_read chars=0 valid=false complete=false receipt_safe=true",
			name,
		)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name+"-artifact"), body, 0o600); err != nil {
		t.Errorf(
			"provider=%s status=invalid kind=output_write chars=0 valid=false complete=true receipt_safe=true",
			name,
		)
	}
}

type liveWorkerConverter struct {
	worker *harvestpy.Converter
	dir    string
}

func (converter *liveWorkerConverter) Convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	ext := filepath.Ext(source)
	if ext == "" {
		ext = "." + strings.TrimPrefix(strings.ToLower(kind), ".")
	}
	file, err := os.CreateTemp(converter.dir, "document-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create live conversion input: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	converted, err := converter.worker.Convert(ctx, harvestpy.Request{Path: path, Kind: kind, Source: source})
	if err != nil {
		return "", err
	}
	return converted.Markdown, nil
}
