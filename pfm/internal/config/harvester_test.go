package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestHarvesterDefaultsWhenFileAbsent(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), FileName)
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	h := got.Harvester
	if h.Exists || h.Enabled || h.External.Enabled || h.External.Port != DefaultHarvesterExternalPort ||
		h.External.Host != "127.0.0.1" || !h.Search.Enabled || h.Cache.TTL != 24*time.Hour ||
		h.Cache.NegativeTTL != 120*time.Second || h.Cache.NegativeTransientTTL != 15*time.Second ||
		h.Scholarly != (HarvesterScholarly{}) ||
		h.Output.MaxInlineChars != 50000 || h.Cache.Dir != "" {
		t.Fatalf("defaults = %+v", h)
	}
	if got.MCP.HTTP.Port != DefaultMCPPort {
		t.Fatalf("loopback port = %d, want %d", got.MCP.HTTP.Port, DefaultMCPPort)
	}
	if h.Path != filepath.Join(filepath.Dir(path), HarvesterFileName) {
		t.Fatalf("harvester path = %q", h.Path)
	}
}

func TestHarvesterFileLoadsEverySetting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{
  "enabled": true,
  "external": {"enabled": true, "host": "0.0.0.0", "port": 19000, "publicURL": "https://harvester.example.com/",
               "auth": {"passphrase": "open sesame", "staticToken": "tok"}, "stateDir": "~/state"},
  "search": {"enabled": true, "searxngURL": "http://127.0.0.1:8888/", "braveApiKey": "brave"},
	  "scholarly": {"contactEmail": "ops@example.com", "googleBooksApiKey": "g", "coreApiKey": "c", "semanticScholarApiKey": "s", "googleScholarURL": "https://scholar.example", "mirrors": {"doi-mirror": "  https://mirror.example/base  ", "ipfs-catalog": "https://ipfs-catalog.example", "doi-viewer": "https://doi-viewer.example", "md5-catalog": "https://md5-catalog.example"}},
  "fetch": {"browser": true, "userAgent": "UA/1", "proxyURL": "http://proxy.example:3128"},
  "convert": {"pdfOcr": true, "pdfLayout": true},
  "cache": {"dir": "~/cache", "ttlSeconds": 60, "negativeTtlSeconds": 5, "negativeTransientTtlSeconds": 2},
  "output": {"maxInlineChars": 1234}
}`, 0o600)
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	h := got.Harvester
	if !h.Enabled || !got.MCPServers["harvester"].Enabled {
		t.Fatalf("enabled not mirrored: harvester=%t server=%t", h.Enabled, got.MCPServers["harvester"].Enabled)
	}
	want := HarvesterExternal{
		Enabled: true, Host: "0.0.0.0", Port: 19000, PublicURL: "https://harvester.example.com",
		Passphrase: "open sesame", StaticToken: "tok", StateDir: filepath.Join(home, "state"),
	}
	if h.External != want {
		t.Fatalf("external = %+v, want %+v", h.External, want)
	}
	if h.Search.SearXNGURL != "http://127.0.0.1:8888" || h.Search.BraveAPIKey != "brave" {
		t.Fatalf("search = %+v", h.Search)
	}
	if h.Scholarly != (HarvesterScholarly{ContactEmail: "ops@example.com", GoogleBooksAPIKey: "g", CoreAPIKey: "c", SemanticScholarAPIKey: "s", DOIMirrorURL: "https://mirror.example/base", IPFSCatalogURL: "https://ipfs-catalog.example", DOIViewerURL: "https://doi-viewer.example", MD5CatalogURL: "https://md5-catalog.example", GoogleScholarURL: "https://scholar.example"}) {
		t.Fatalf("scholarly = %+v", h.Scholarly)
	}
	if !h.Fetch.Browser || h.Fetch.UserAgent != "UA/1" || h.Fetch.ProxyURL != "http://proxy.example:3128" {
		t.Fatalf("fetch = %+v", h.Fetch)
	}
	if h.Cache.Dir != filepath.Join(home, "cache") || h.Cache.TTL != time.Minute ||
		h.Cache.NegativeTTL != 5*time.Second ||
		h.Cache.NegativeTransientTTL != 2*time.Second {
		t.Fatalf("cache = %+v", h.Cache)
	}
	if !h.Convert.PDFOCR || !h.Convert.PDFLayout {
		t.Fatalf("convert = %+v", h.Convert)
	}
	if h.Output.MaxInlineChars != 1234 {
		t.Fatalf("output = %+v", h.Output)
	}
	for _, key := range HarvesterSourceKeys() {
		if got.Source(key) != SourceFile {
			t.Errorf("source %s = %q, want file", key, got.Source(key))
		}
	}
}

func TestHarvesterDOIMirrorURLRoundTripsWithoutChangingSiblingSettings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{
  "enabled": true,
  "search": {"enabled": false, "searxngURL": "http://127.0.0.1:8888"},
	  "scholarly": {"contactEmail": "ops@example.com", "googleScholarURL": "https://scholar.example", "mirrors": {"doi-mirror": " https://mirror.example/doi-mirror ", "ipfs-catalog": " https://ipfs-catalog.example ", "doi-viewer": "https://doi-viewer.example", "md5-catalog": "https://md5-catalog.example"}},
  "fetch": {"userAgent": "fixture-agent"},
  "cache": {"ttlSeconds": 77},
  "output": {"maxInlineChars": 321}
}`, 0o600)
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Harvester.Scholarly.DOIMirrorURL != "https://mirror.example/doi-mirror" ||
		got.Harvester.Scholarly.IPFSCatalogURL != "https://ipfs-catalog.example" ||
		got.Harvester.Scholarly.DOIViewerURL != "https://doi-viewer.example" ||
		got.Harvester.Scholarly.MD5CatalogURL != "https://md5-catalog.example" ||
		got.Harvester.Scholarly.GoogleScholarURL != "https://scholar.example" {
		t.Fatalf("DOIMirrorURL = %q, want trimmed configured URL", got.Harvester.Scholarly.DOIMirrorURL)
	}
	if got.Harvester.Search.Enabled || got.Harvester.Search.SearXNGURL != "http://127.0.0.1:8888" ||
		got.Harvester.Scholarly.ContactEmail != "ops@example.com" || got.Harvester.Fetch.UserAgent != "fixture-agent" ||
		got.Harvester.Cache.TTL != 77*time.Second || got.Harvester.Output.MaxInlineChars != 321 {
		t.Fatalf("unrelated settings changed: %+v", got.Harvester)
	}
	if got.Source("harvester.scholarly.mirrors") != SourceFile {
		t.Fatalf("mirrors source = %q, want %q", got.Source("harvester.scholarly.mirrors"), SourceFile)
	}
	marshaled, err := MarshalHarvester(got.Harvester, false)
	if err != nil {
		t.Fatalf("MarshalHarvester() error = %v", err)
	}
	var shape struct {
		Search    map[string]any `json:"search"`
		Scholarly map[string]any `json:"scholarly"`
	}
	if err := json.Unmarshal(marshaled, &shape); err != nil {
		t.Fatalf("MarshalHarvester JSON = %v", err)
	}
	if shape.Scholarly["googleScholarURL"] != "https://scholar.example" {
		t.Fatalf("marshaled googleScholarURL = %#v", shape.Scholarly["googleScholarURL"])
	}
	if _, shown := shape.Scholarly["mirrors"]; shown || strings.Contains(string(marshaled), "mirror.example") {
		t.Fatalf("marshaled config exposes operator mirror URLs: %s", marshaled)
	}
	if shape.Search["searxngURL"] != "http://127.0.0.1:8888" || shape.Search["enabled"] != false {
		t.Fatalf("marshaled sibling settings changed: %#v", shape.Search)
	}
}

func TestHarvesterFileRefusesUnsafeOrInvalidSettings(t *testing.T) {
	cases := map[string]struct {
		content string
		mode    os.FileMode
		want    string
	}{
		"external without auth": {
			`{"external":{"enabled":true,"publicURL":"https://h.example.com"}}`,
			0o600,
			"never unauthenticated",
		},
		"external without url": {
			`{"external":{"enabled":true,"auth":{"staticToken":"t"}}}`,
			0o600,
			"requires external.publicURL",
		},
		"external url path": {`{"external":{"publicURL":"https://h.example.com/mcp"}}`, 0o600, "without a path"},
		"searxng query":     {`{"search":{"searxngURL":"http://127.0.0.1:8888/?x=1"}}`, 0o600, "query or fragment"},
		"searxng scheme":    {`{"search":{"searxngURL":"ftp://127.0.0.1"}}`, 0o600, "http or https"},
		"doi-mirror scheme": {
			`{"scholarly":{"mirrors":{"doi-mirror":"ftp://mirror.example"}}}`,
			0o600,
			"http or https",
		},
		"doi-mirror relative": {
			`{"scholarly":{"mirrors":{"doi-mirror":"mirror.example/path"}}}`,
			0o600,
			"http or https",
		},
		"doi-mirror userinfo": {
			`{"scholarly":{"mirrors":{"doi-mirror":"https://user:pass@mirror.example"}}}`,
			0o600,
			"must not carry userinfo",
		},
		"doi-mirror query": {
			`{"scholarly":{"mirrors":{"doi-mirror":"https://mirror.example/?token=x"}}}`,
			0o600,
			"query or fragment",
		},
		"doi-mirror fragment": {
			`{"scholarly":{"mirrors":{"doi-mirror":"https://mirror.example/#pdf"}}}`,
			0o600,
			"query or fragment",
		},
		"negative ttl":          {`{"cache":{"ttlSeconds":-1}}`, 0o600, "0 or more"},
		"zero inline":           {`{"output":{"maxInlineChars":0}}`, 0o600, "at least 1"},
		"relative cache dir":    {`{"cache":{"dir":"cache"}}`, 0o600, "must be absolute"},
		"unknown key":           {`{"search":{"searxng":"http://x"}}`, 0o600, "unknown field"},
		"retired env name":      {`{"SEARXNG_URL":"http://x"}`, 0o600, "unknown field"},
		"world-readable secret": {`{"search":{"braveApiKey":"k"}}`, 0o644, "chmod 600"},
		"port collision": {
			`{"external":{"enabled":true,"port":18377,"publicURL":"https://h.example.com","auth":{"staticToken":"t"}}}`,
			0o600,
			"collides with mcp.http.port",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, HarvesterFileName), test.content, test.mode)
			_, err := Load(filepath.Join(dir, FileName), filepath.Join(dir, "home"), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestHarvesterScholarlyProviderURLsRejectUnsafeComponents(t *testing.T) {
	for _, field := range []string{"mirrors.ipfs-catalog", "mirrors.doi-viewer", "mirrors.md5-catalog", "googleScholarURL"} {
		for name, value := range map[string]string{
			"scheme":   "ftp://mirror.example",
			"userinfo": "https://user:pass@mirror.example",
			"query":    "https://mirror.example/?token=x",
			"fragment": "https://mirror.example/#pdf",
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				content := `{"scholarly":{"` + field + `":"` + value + `"}}`
				if id, ok := strings.CutPrefix(field, "mirrors."); ok {
					content = `{"scholarly":{"mirrors":{"` + id + `":"` + value + `"}}}`
				}
				writeFile(t, filepath.Join(dir, HarvesterFileName), content, 0o600)
				if _, err := Load(filepath.Join(dir, FileName), filepath.Join(dir, "home"), nil); err == nil {
					t.Fatalf("Load accepted unsafe %s=%q", field, value)
				}
			})
		}
	}
}

func TestHarvesterDOIMirrorURLWhitespaceOnlyDisablesFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{"scholarly":{"mirrors":{"doi-mirror":"   "}}}`, 0o600)
	got, err := Load(filepath.Join(dir, FileName), filepath.Join(dir, "home"), nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Harvester.Scholarly.DOIMirrorURL != "" {
		t.Fatalf("DOIMirrorURL = %q, want empty disabled value", got.Harvester.Scholarly.DOIMirrorURL)
	}
}

func TestHarvesterWorldReadableWithoutSecretsLoads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{"search":{"searxngURL":"http://127.0.0.1:8888"}}`, 0o644)
	if _, err := Load(filepath.Join(dir, FileName), filepath.Join(dir, "home"), nil); err != nil {
		t.Fatalf("a secret-free harvester file must load at 0644: %v", err)
	}
}

func TestHarvesterFileEnabledWinsOverLegacyKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, path, `{"version":2,"mcp":{"servers":{"harvester":{"enabled":true}}}}`, 0o600)
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{"enabled":false}`, 0o600)
	got, err := Load(path, filepath.Join(dir, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Harvester.Enabled || got.MCPServers["harvester"].Enabled || got.MCPServerSource("harvester") != SourceFile {
		t.Fatalf("harvester enabled=%t server=%t source=%q, want the harvester file's false",
			got.Harvester.Enabled, got.MCPServers["harvester"].Enabled, got.MCPServerSource("harvester"))
	}
}

func TestLoadFallsBackToPreSplitFileUntilMigrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	legacy := filepath.Join(home, ".config", "pfm", LegacyFileName)
	writeFile(t, legacy, `{"version":2,"theme":"tokyo-night"}`, 0o600)
	got, err := Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != legacy || !got.Exists || got.Theme != "tokyo-night" {
		t.Fatalf("pre-split fallback: path=%q exists=%t theme=%q", got.Path, got.Exists, got.Theme)
	}
	current := filepath.Join(home, ".config", "pfm", FileName)
	writeFile(t, current, `{"version":2,"theme":"default"}`, 0o600)
	got, err = Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != current || got.Theme != "default" {
		t.Fatalf("current file must win once present: path=%q theme=%q", got.Path, got.Theme)
	}
}

func TestSetMCPServerHarvesterWritesHarvesterFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, path, `{"version":2}`, 0o600)
	writeFile(t, filepath.Join(dir, HarvesterFileName), `{"search":{"searxngURL":"http://127.0.0.1:8888"}}`, 0o600)
	loaded, err := Load(path, filepath.Join(dir, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := SetMCPServer(loaded, "harvester", true)
	if err != nil || !changed {
		t.Fatalf("SetMCPServer = %t, %v", changed, err)
	}
	pfmContent, _ := os.ReadFile(path)
	if strings.Contains(string(pfmContent), "harvester") {
		t.Fatalf("pfm.config.json gained a harvester key:\n%s", pfmContent)
	}
	var harvester map[string]any
	content, _ := os.ReadFile(filepath.Join(dir, HarvesterFileName))
	if err := json.Unmarshal(content, &harvester); err != nil {
		t.Fatal(err)
	}
	if harvester["enabled"] != true || harvester["search"] == nil {
		t.Fatalf("harvester file = %s", content)
	}
}

func TestHarvesterSecretsNeverRenderInDisplay(t *testing.T) {
	harvester := DefaultHarvester()
	harvester.External.Passphrase = "open sesame"
	harvester.External.StaticToken = "example-tok-123"
	harvester.Search.BraveAPIKey = "example-brave-456"
	harvester.Scholarly.CoreAPIKey = "example-core-789"
	content, err := MarshalHarvester(harvester, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"open sesame", "example-tok-123", "example-brave-456", "example-core-789"} {
		if strings.Contains(string(content), secret) {
			t.Fatalf("redacted harvester output leaks %q:\n%s", secret, content)
		}
	}
	generic := RedactSecrets([]byte(`{"braveApiKey":"b","auth":{"passphrase":"p"}}`))
	if strings.Contains(string(generic), `"b"`) || strings.Contains(string(generic), `"p"`) {
		t.Fatalf("RedactSecrets leaked apikey/passphrase: %s", generic)
	}
}
