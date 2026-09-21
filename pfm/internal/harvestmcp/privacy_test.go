package harvestmcp

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

func TestDescribeFetchRedactsProviderDiagnosticsAtMCPBoundary(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	got := (*Service)(nil).describeFetch("10.1234/public.boundary", harvest.Result{
		Source:    "https://mirror.secret.example/private",
		Error:     "GET https://mirror.secret.example/private: provider internals",
		ErrorKind: "connect",
	}, false)
	if strings.Contains(got, "mirror.secret.example") || strings.Contains(got, "provider internals") {
		t.Fatalf("MCP receipt exposed provider diagnostics: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "connection failed") {
		t.Fatalf("MCP receipt lost the safe outage class: %q", got)
	}
}

func TestFetchItemCarriesOnlyPublicResultFields(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	item := fetchItem(harvest.Result{
		Source:      "10.1234/public.boundary",
		Content:     "article",
		Path:        "/cache/public/opaque.md",
		CacheStatus: "hit",
		Bytes:       7,
		Method:      "doi-mirror",
		Rungs:       []string{"direct", "mirror:https://mirror.secret.example"},
	})
	if item.Source != "10.1234/public.boundary" || item.Path != "/cache/public/opaque.md" || item.Content != "article" {
		t.Fatalf("public fetch item changed its public fields: %#v", item)
	}
	if strings.Contains(item.Path, "mirror.secret.example") || strings.Contains(item.Content, "mirror.secret.example") {
		t.Fatalf("fetch item exposed provider details: %#v", item)
	}
}
