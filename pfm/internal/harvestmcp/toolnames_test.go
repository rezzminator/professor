package harvestmcp

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// TestToolNamesFollowTheSearchGate pins RegisteredToolNames to the same search gate
// register() applies (service.go:493): a runtime with no SearXNG URL or
// Brave key omits `search_web` entirely, and a configured runtime lists it
// after search_literature — the same slot register() adds it in.
func TestToolNamesFollowTheSearchGate(t *testing.T) {
	off := Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")}
	wantOff := []string{"read", "download_file", "search_literature"}
	if got := RegisteredToolNames(off); !reflect.DeepEqual(got, wantOff) {
		t.Fatalf("RegisteredRegisteredToolNames(no backend) = %#v, want %#v", got, wantOff)
	}

	on := Runtime{
		Home:       t.TempDir(),
		CacheDir:   filepath.Join(t.TempDir(), "cache"),
		SearXNGURL: "http://searxng.example.test",
	}
	wantOn := []string{"read", "download_file", "search_literature", "search_web"}
	if got := RegisteredToolNames(on); !reflect.DeepEqual(got, wantOn) {
		t.Fatalf("RegisteredRegisteredToolNames(SearXNGURL configured) = %#v, want %#v", got, wantOn)
	}
}

// TestToolNamesMatchTheRegisteredServer is the strong form of the gate test
// above: rather than pinning RegisteredToolNames' own output, it builds a real Service
// for both the search-off and search-on runtimes and compares RegisteredRegisteredToolNames(runtime)
// against the names the SDK's tools/list actually returns (via listToolNames,
// the in-process client helper service_test.go's search-gate tests already
// use). The SDK's featureSet advertises tools/list alphabetically
// (TestStableFourToolSurface pins that), while RegisteredToolNames returns
// register()'s own order per the brief above, so the two are compared as
// sets, sorted, not as an ordered sequence — the membership is what /status
// must never drift from, not the SDK's internal listing order. RegisteredToolNames can
// still drift from register() only if both sides move and stay wrong the
// same way, which this test would catch since it reads the registered
// server directly, never RegisteredToolNames' own claim.
func TestToolNamesMatchTheRegisteredServer(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime Runtime
	}{
		{
			name:    "search disabled",
			runtime: Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
		},
		{
			name: "search enabled",
			runtime: Runtime{
				Home:       t.TempDir(),
				CacheDir:   filepath.Join(t.TempDir(), "cache"),
				SearXNGURL: "http://searxng.example.test",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewConfiguredHarvester("test", test.runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = service.Close() }()
			registered := listToolNames(t, service)
			want := RegisteredToolNames(test.runtime)
			sortedRegistered := slices.Clone(registered)
			slices.Sort(sortedRegistered)
			sortedWant := slices.Clone(want)
			slices.Sort(sortedWant)
			if !reflect.DeepEqual(sortedRegistered, sortedWant) {
				t.Fatalf("registered tools = %#v, RegisteredRegisteredToolNames(runtime) = %#v", registered, want)
			}
		})
	}
}
