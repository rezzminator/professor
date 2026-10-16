package harvest

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// TestNewResolvesNoConfiguredMirror: building a Harvester checks each
// configured provider base URL without a DNS lookup, so a mirror whose name no
// longer resolves costs nothing until a fetch uses it; a base URL that names a
// private host by itself is still refused at startup.
func TestNewResolvesNoConfiguredMirror(t *testing.T) {
	var lookups atomic.Int32
	previous := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		lookups.Add(1)
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	t.Cleanup(func() { lookupIP = previous })
	options := Options{
		CacheDir:         t.TempDir(),
		Converter:        &fakeConverter{},
		DOIMirrorURL:     "https://doi-mirror.example.net",
		IPFSCatalogURL:   "https://ipfs-catalog.example.net",
		DOIViewerURL:     "https://doi-viewer.example.net",
		MD5CatalogURL:    "https://md5-catalog.example.net",
		GoogleScholarURL: "https://scholar.example.net",
	}
	if _, err := New(options); err != nil {
		t.Fatalf("New with unresolvable mirrors: %v", err)
	}
	if got := lookups.Load(); got != 0 {
		t.Fatalf("New made %d DNS lookups for its configured mirrors, want none", got)
	}
	options.MD5CatalogURL = "http://127.0.0.1:8080"
	if _, err := New(
		options,
	); err == nil ||
		!strings.Contains(err.Error(), "refusing private/internal host 127.0.0.1") {
		t.Fatalf("New with a private literal mirror = %v, want the private-host refusal", err)
	}
}
