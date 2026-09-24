package harvestmcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestLocalDownloadAnswersAnAbsolutePathAndNoURL: on the local server a
// download is the absolute path of the stored file, which exists — even when
// the store handed back a relative one — and never a url or id.
func TestLocalDownloadAnswersAnAbsolutePathAndNoURL(t *testing.T) {
	service := newTestService(t, Runtime{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "paper.pdf"), []byte("%PDF-1.4\nlocal"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	item := service.downloadItem(context.Background(), "https://example.test/paper.pdf",
		harvest.Result{Kind: "pdf", Path: "./sub/../paper.pdf", Method: "direct"})
	if item.Error != "" || !filepath.IsAbs(item.Path) || filepath.Clean(item.Path) != item.Path {
		t.Fatalf("local item = %+v, want an absolute clean path", item)
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatalf("local path %q does not exist: %v", item.Path, err)
	}
	if item.URL != "" || item.ID != "" || item.Expires != "" || item.Resource != nil {
		t.Fatalf("local item carries remote fields: %+v", item)
	}
}

type filesGateway struct {
	server *RemoteServer
	clock  *clock.Fake
	body   []byte
	item   DownloadItem
}

func newFilesGateway(t *testing.T) filesGateway {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	base := t.TempDir()
	server, err := NewRemote(RemoteOptions{
		Runtime:     Runtime{Home: base, CacheDir: filepath.Join(base, "cache"), Clock: fake},
		PublicURL:   "https://harvester.example.test",
		StaticToken: "example-fixture-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	body := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("0123456789abcdef"), 2<<20/16)...)
	pdf := filepath.Join(base, "paper.pdf")
	if err := os.WriteFile(pdf, body, 0o600); err != nil {
		t.Fatal(err)
	}
	item := server.service.downloadItem(context.Background(), "https://example.test/papers/paper.pdf",
		harvest.Result{Kind: "pdf", Path: pdf, Method: "direct"})
	return filesGateway{server: server, clock: fake, body: body, item: item}
}

func (gateway filesGateway) get(t *testing.T, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, http.NoBody)
	req.Host = "harvester.example.test"
	for name, values := range header {
		req.Header[name] = values
	}
	rec := httptest.NewRecorder()
	gateway.server.Handler().ServeHTTP(rec, req)
	return rec
}

// TestRemoteDownloadAnswersASignedExpiringURL: the remote item is id, url,
// expires and the resource_link, never a server path; the whole tool result
// for a 2 MB PDF stays under 1 KB, so the bytes never reach the model.
func TestRemoteDownloadAnswersASignedExpiringURL(t *testing.T) {
	gateway := newFilesGateway(t)
	item := gateway.item
	sum := sha256.Sum256(gateway.body)
	id := hex.EncodeToString(sum[:])
	if item.Error != "" || item.Path != "" || item.ID != id || item.SHA256 != id || item.Resource == nil ||
		item.ContentType != "application/pdf" || item.Bytes != int64(len(gateway.body)) {
		t.Fatalf("remote item = %+v", item)
	}
	if want := gateway.clock.Now().Add(downloadLinkTTL).UTC().Format(time.RFC3339); item.Expires != want {
		t.Fatalf("expires = %q, want %q", item.Expires, want)
	}
	if !strings.HasPrefix(item.URL, "https://harvester.example.test/files/"+id+"?exp=") ||
		!strings.Contains(item.URL, "&sig=") {
		t.Fatalf("url = %q", item.URL)
	}
	structured, err := json.Marshal(DownloadOutput{Items: []DownloadItem{item}})
	if err != nil {
		t.Fatal(err)
	}
	result := downloadResult([]DownloadItem{item})
	result.StructuredContent = DownloadOutput{Items: []DownloadItem{item}}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	// The typed result stays under 1 KB; the whole wire result (text render +
	// resource_link block + structuredContent) under 2 KB, for a 2 MB file.
	if len(structured) >= 1024 || len(wire) >= 2048 || bytes.Contains(wire, []byte("JVBERi0")) ||
		bytes.Contains(wire, []byte(filepath.Dir(gateway.server.service.runtime.CacheDir))) {
		t.Fatalf("tool result is %d bytes, structured %d (want < 2048 / < 1024, no bytes, no server path): %s",
			len(wire), len(structured), wire)
	}
	if !strings.Contains(downloadDescription, "curl -fL -o <file> <url>") ||
		!strings.Contains(downloadDescription, "restart") {
		t.Fatalf("download description does not say how to fetch the url: %s", downloadDescription)
	}
}

// TestFilesRouteServesOnlySignedLiveStoreHeldIDs: the /files route needs no
// bearer, answers the exact bytes and ranges, and refuses a tampered, expired,
// unknown or malformed link by its own status; /mcp still needs the bearer.
func TestFilesRouteServesOnlySignedLiveStoreHeldIDs(t *testing.T) {
	gateway := newFilesGateway(t)
	link, err := url.Parse(gateway.item.URL)
	if err != nil {
		t.Fatal(err)
	}
	target := link.RequestURI()

	rec := gateway.get(t, http.MethodGet, target, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), gateway.body) {
		t.Fatalf("GET = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	for name, want := range map[string]string{
		"Content-Type":           "application/pdf",
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `attachment; filename="paper.pdf"`,
	} {
		if got := rec.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if sum := sha256.Sum256(rec.Body.Bytes()); hex.EncodeToString(sum[:]) != gateway.item.SHA256 {
		t.Fatal("served bytes do not match the item's sha256")
	}
	rec = gateway.get(t, http.MethodGet, target, http.Header{"Range": {"bytes=10-19"}})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), gateway.body[10:20]) {
		t.Fatalf("Range = %d %q", rec.Code, rec.Body.Bytes())
	}
	rec = gateway.get(t, http.MethodHead, target, nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 ||
		rec.Header().Get("Content-Length") != strconv.Itoa(len(gateway.body)) {
		t.Fatalf("HEAD = %d len=%s body=%d", rec.Code, rec.Header().Get("Content-Length"), rec.Body.Len())
	}

	sig := link.Query().Get("sig")
	tampered := strings.Replace(target, sig, flipLast(sig), 1)
	unsigned := strings.Split(target, "&sig=")[0]
	unknown, _ := gateway.server.service.links.link(strings.Repeat("0", 64))
	unknownURL, err := url.Parse(unknown)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name, target string
		want         int
	}{
		{"tampered sig", tampered, http.StatusForbidden},
		{"missing sig", unsigned, http.StatusForbidden},
		{"unknown id", unknownURL.RequestURI(), http.StatusNotFound},
		{"traversal", "/files/..%2f..%2fetc%2fpasswd?" + link.RawQuery, http.StatusNotFound},
		{"non-hex id", "/files/not-a-sha256?" + link.RawQuery, http.StatusNotFound},
		{"listing", "/files/", http.StatusNotFound},
	} {
		rec := gateway.get(t, http.MethodGet, check.target, nil)
		body := rec.Body.String()
		if rec.Code != check.want || strings.Contains(body, "passwd") || strings.Contains(body, sig) ||
			strings.Contains(body, gateway.server.service.runtime.CacheDir) || strings.TrimSpace(body) == "" {
			t.Fatalf("%s: %d %q, want %d with a named cause", check.name, rec.Code, body, check.want)
		}
	}

	if rec := gateway.get(t, http.MethodGet, "/mcp", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("/mcp without bearer = %d, want 401", rec.Code)
	}

	gateway.clock.Advance(downloadLinkTTL + time.Second)
	rec = gateway.get(t, http.MethodGet, target, nil)
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("expired = %d %q, want 410", rec.Code, rec.Body.String())
	}
}

// TestRemoteDownloadWithoutAPublicURLSaysSo: a remote service with no link
// signer names the missing setting and keeps only the resource_link.
func TestRemoteDownloadWithoutAPublicURLSaysSo(t *testing.T) {
	service := newTestService(t, Runtime{Remote: true})
	pdf := filepath.Join(t.TempDir(), "a.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\nx"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := service.downloadItem(context.Background(), "https://example.test/a.pdf",
		harvest.Result{Kind: "pdf", Path: pdf})
	if item.URL != "" || item.Resource == nil || !strings.Contains(item.Note, "external.publicURL") {
		t.Fatalf("item = %+v", item)
	}
}

func flipLast(sig string) string {
	if strings.HasSuffix(sig, "0") {
		return sig[:len(sig)-1] + "1"
	}
	return sig[:len(sig)-1] + "0"
}
