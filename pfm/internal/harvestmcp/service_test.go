package harvestmcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
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
	defer func() { _ = service.Close() }()
	got := listToolNames(t, service)
	want := []string{"harvester_download_file", "harvester_read", "harvester_search_literature", "harvester_search_web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
}

// listToolNames connects an in-process client to the service and reads the
// registered surface for the tool-surface tests.
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

// TestServiceCacheIsTheOneRootNotTheWorkingDirectory pins the split-cache
// defect: NewConfigured resolved a cwd-relative ".cache", so the daemon
// (systemd cwd = $HOME) cached into ~/.cache while the CLI used the one
// default root (now ~/.professor/.harvester-cache) and the two never shared a
// hit.
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
	if want := filepath.Join(home, ".professor", ".harvester-cache"); service.runtime.CacheDir != want {
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

// convertFunc adapts a function to harvest.Converter.
type convertFunc func(ctx context.Context, kind, source string, body []byte) (string, error)

func (convert convertFunc) Convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	return convert(ctx, kind, source, body)
}

// TestLoadRefusalsStayOutOfTheCache: the converter pool's busy refusal and
// browser render slots that stayed full are the server's load, so the read
// after them walks again (noteLoadRefusal, browserSlot); a conversion its
// deadline cut (the pool wraps the caller's context error) is the document's
// walk failing and stays the source's cached failure.
func TestLoadRefusalsStayOutOfTheCache(t *testing.T) {
	t.Parallel()
	convertFailure := func(err error) func(context.Context) error {
		return func(ctx context.Context) error {
			noteLoadRefusal(ctx, err)
			return err
		}
	}
	cases := map[string]struct {
		fail       func(context.Context) error
		wantCached bool
	}{
		"converter busy walks again": {convertFailure(fmt.Errorf("acquire: %w", harvestpy.ErrConverterBusy)), false},
		"conversion deadline stays cached": {
			convertFailure(fmt.Errorf("harvestpy worker request cancelled: %w", context.DeadlineExceeded)), true,
		},
		"render slots full walks again": {func(ctx context.Context) error {
			full := pythonConverter{browserSlots: make(chan struct{}, 1)}
			full.browserSlots <- struct{}{}
			_, err := full.browserSlot(ctx, time.Millisecond)
			return err
		}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var failing atomic.Bool
			failing.Store(true)
			harvester, err := harvest.New(harvest.Options{
				CacheDir: t.TempDir(),
				Converter: convertFunc(func(ctx context.Context, _, _ string, _ []byte) (string, error) {
					if failing.Load() {
						return "", tc.fail(ctx)
					}
					return strings.Repeat("real article content ", 80), nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "doc.html")
			body := "<html><body>" + strings.Repeat("real article content ", 80) + "</body></html>"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if first := harvester.Fetch(context.Background(), path); first.Error == "" {
				t.Fatal("Fetch() with a failing converter returned no error")
			}
			failing.Store(false)
			second := harvester.Fetch(context.Background(), path)
			if cached := second.Error != ""; cached != tc.wantCached {
				t.Fatalf("read after the failure: cached = %v (Error=%q), want %v", cached, second.Error, tc.wantCached)
			}
		})
	}
}
