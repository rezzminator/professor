package harvestmcp

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestStableFourToolSurface(t *testing.T) {
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{
			Home:       t.TempDir(),
			CacheDir:   filepath.Join(t.TempDir(), "cache"),
			SearXNGURL: "http://searxng.example.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := serverSession.Close(); err != nil {
			t.Errorf("close serverSession: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		got = append(got, tool.Name)
	}
	want := []string{"harvester_download_file", "harvester_read", "harvester_search_literature", "harvester_search_web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
}

// listToolNames connects an in-process client to the given service and
// returns the tool names it advertises — the one place both search-gating
// tests below read the registered surface, rather than poking register()
// internals.
func listToolNames(t *testing.T, service *Service) []string {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := serverSession.Close(); err != nil {
			t.Errorf("close serverSession: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestSearchToolHiddenWithoutABackend is the regression for a `harvester_search_web` tool
// advertised with nowhere to search: register() used to gate only on
// !DisableSearch, so a Service with neither SearXNGURL nor BraveAPIKey set
// still listed `harvester_search_web`, and calling it always failed with a configuration
// error the caller had no way to see in advance.
func TestSearchToolHiddenWithoutABackend(t *testing.T) {
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	names := listToolNames(t, service)
	for _, name := range names {
		if name == "harvester_search_web" {
			t.Fatalf("tool list %v advertises `harvester_search_web` with no backend configured", names)
		}
	}
}

// TestSearchToolListedWithSearXNGConfigured is TestSearchToolHiddenWithoutABackend's
// positive twin: a configured backend must still register the tool.
func TestSearchToolListedWithSearXNGConfigured(t *testing.T) {
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{
			Home:       t.TempDir(),
			CacheDir:   filepath.Join(t.TempDir(), "cache"),
			SearXNGURL: "http://searxng.example.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	names := listToolNames(t, service)
	found := false
	for _, name := range names {
		if name == "harvester_search_web" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool list %v does not advertise `harvester_search_web` with SearXNGURL configured", names)
	}
}

// TestServiceCacheIsTheOneRootNotTheWorkingDirectory pins the split-cache
// defect: NewConfigured resolved a cwd-relative ".cache", so the daemon
// (systemd cwd = $HOME) cached into ~/.cache while the CLI used
// ~/.professor/.cache and the two never shared a hit.
func TestServiceCacheIsTheOneRootNotTheWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(paths.EnvHome, home)
	t.Setenv("WEBFETCH_DIR", filepath.Join(t.TempDir(), "legacy"))
	service, err := NewConfiguredHarvester("test", Runtime{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	if want := filepath.Join(home, ".professor", ".cache"); service.runtime.CacheDir != want {
		t.Fatalf("service cache root = %q, want the one default %q", service.runtime.CacheDir, want)
	}
}

func TestConfiguredServiceCarriesScholarlyProviderRuntime(t *testing.T) {
	home := t.TempDir()
	service, err := NewConfiguredHarvester("test", Runtime{
		Home:             home,
		CacheDir:         filepath.Join(home, "cache"),
		DOIMirrorURL:     "https://mirror.example/doi-mirror",
		IPFSCatalogURL:   "https://ipfs-catalog.example",
		DOIViewerURL:     "https://doi-viewer.example",
		MD5CatalogURL:    "https://md5-catalog.example",
		GoogleScholarURL: "https://scholar.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	for _, tc := range []struct{ name, got, want string }{
		{"DOIMirrorURL", service.runtime.DOIMirrorURL, "https://mirror.example/doi-mirror"},
		{"IPFSCatalogURL", service.runtime.IPFSCatalogURL, "https://ipfs-catalog.example"},
		{"DOIViewerURL", service.runtime.DOIViewerURL, "https://doi-viewer.example"},
		{"MD5CatalogURL", service.runtime.MD5CatalogURL, "https://md5-catalog.example"},
		{"GoogleScholarURL", service.runtime.GoogleScholarURL, "https://scholar.example"},
	} {
		if tc.got != tc.want {
			t.Errorf("service runtime %s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
