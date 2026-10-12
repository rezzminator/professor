package harvestmcp

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

func TestRenderReadItemRedactsProviderDiagnosticsAtMCPBoundary(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	got, _ := RenderReadItem(1, 1, "10.1234/public.boundary", harvest.Result{
		Source:    "https://mirror.secret.example/private",
		Error:     "GET https://mirror.secret.example/private: provider internals",
		ErrorKind: "connect",
	}, ReadView{})
	if strings.Contains(got, "mirror.secret.example") || strings.Contains(got, "provider internals") {
		t.Fatalf("MCP answer exposed provider diagnostics: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "connection failed") {
		t.Fatalf("MCP answer lost the safe outage class: %q", got)
	}
}

// TestRenderReadItemCarriesOnlyPublicResultFields: a read block names the item
// as passed, its public path and its content — never the method or rung trace
// that names a provider.
func TestRenderReadItemCarriesOnlyPublicResultFields(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	got, failed := RenderReadItem(1, 1, "10.1234/public.boundary", harvest.Result{
		Source:      "10.1234/public.boundary",
		Content:     "article",
		Chars:       7,
		Path:        "/cache/public/opaque.md",
		CacheStatus: "hit",
		Bytes:       7,
		Method:      "doi-mirror",
		Rungs:       []string{"direct", "mirror:https://mirror.secret.example"},
	}, ReadView{})
	if failed || got != "=== [1/1] 10.1234/public.boundary\n/cache/public/opaque.md\narticle" {
		t.Fatalf("public read block = %q", got)
	}
}
