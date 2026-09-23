package harvestmcp

import (
	"encoding/json"
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

func TestPageItemCarriesOnlyPublicResultFields(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	item := (&Service{}).pageItem("10.1234/public.boundary", harvest.Result{
		Source:      "10.1234/public.boundary",
		Content:     "article",
		Path:        "/cache/public/opaque.md",
		CacheStatus: "hit",
		Bytes:       7,
		Method:      "doi-mirror",
		Rungs:       []string{"direct", "mirror:https://mirror.secret.example"},
	}, false)
	if item.Source != "10.1234/public.boundary" || item.Path != "/cache/public/opaque.md" || item.Content != "article" {
		t.Fatalf("public page item changed its public fields: %#v", item)
	}
	if strings.Contains(item.Path, "mirror.secret.example") || strings.Contains(item.Content, "mirror.secret.example") {
		t.Fatalf("page item exposed provider details: %#v", item)
	}
}

// TestPageItemNamesTheRungAndAlwaysCarriesPartial: the page item names the
// rung class that stored the page (a mirror provider only as "mirror") and
// always carries `partial`, empty for a complete artifact.
func TestPageItemNamesTheRungAndAlwaysCarriesPartial(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	item := (&Service{}).pageItem(
		"https://fixture.example/a",
		harvest.Result{Source: "https://fixture.example/a", Method: "doi-mirror"},
		false,
	)
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"method":"mirror"`) || !strings.Contains(string(encoded), `"partial":""`) {
		t.Fatalf("page item lacks method or partial: %s", encoded)
	}
}
