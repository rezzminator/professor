package claudeplugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// One entry whose installPath is not a string reads as not installed and
// costs no other entry of the record.
func TestReadInstallsKeepsAnEntryWithAMalformedInstallPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{`+
		`"broken@x":[{"scope":"user","installPath":7,"version":"1.0.0"}],`+
		`"kept@y":[{"scope":"project","projectPath":"/work","installPath":"/cache/kept","version":"2.0.0"}]}}`)
	got, err := ReadInstalls(dir)
	if err != nil {
		t.Fatalf("ReadInstalls = %v, want the record kept", err)
	}
	want := map[string][]Install{
		"broken@x": {{Scope: "user", Version: "1.0.0"}},
		"kept@y":   {{Scope: "project", ProjectPath: "/work", InstallPath: "/cache/kept", Version: "2.0.0"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

// An entry of any shape names its marketplace for KnownMarketplaceNames, while
// ReadKnownMarketplaces, which types every field, fails the record.
func TestKnownMarketplaceNamesKeepsAnUntypableEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugins", "known_marketplaces.json")
	writeFixture(t, path, `{"odd":{"source":{"source":"github","repo":7}},`+
		`"kept":{"source":{"source":"directory","path":"/m"}}}`)
	got, err := KnownMarketplaceNames(dir)
	if want := map[string]bool{"odd": true, "kept": true}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("KnownMarketplaceNames = %v, %v; want %v, nil", got, err, want)
	}
	if _, known, err := ReadKnownMarketplaces(dir); err == nil {
		t.Fatalf("ReadKnownMarketplaces = %+v, nil; want the parse error of %s", known, path)
	}
	missing, err := KnownMarketplaceNames(t.TempDir())
	if err != nil || len(missing) != 0 {
		t.Fatalf("KnownMarketplaceNames(no file) = %v, %v; want none, nil", missing, err)
	}
}

// Roots names every enabled key of the plugin, sorted, each with the folder
// its marketplace kind loads it from; another plugin's key, a lookalike name
// and a disabled key are not the plugin's.
func TestRootsResolvesEachEnabledKey(t *testing.T) {
	t.Parallel()
	config, market := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{`+
		`"flights@professor":true,"flights@mirror":true,"flights@gone":true,"flights@off":false,`+
		`"flights-x@professor":true,"other@professor":true}}`)
	writeFixture(t, filepath.Join(config, "plugins", "known_marketplaces.json"), `{`+
		`"professor":{"source":{"source":"directory","path":"`+market+`"},"installLocation":"`+market+`"},`+
		`"mirror":{"source":{"source":"github","repo":"o/r"},"installLocation":"/unused"}}`)
	writeFixture(t, filepath.Join(market, ".claude-plugin", "marketplace.json"),
		`{"name":"professor","plugins":[{"name":"flights","source":"./plugins/flights"}]}`)
	writeFixture(t, filepath.Join(config, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"flights@mirror":[{"installPath":"/cache/one"},{"installPath":"/cache/two"}]}}`)
	got, err := Roots(config, "flights")
	if err != nil {
		t.Fatal(err)
	}
	want := []Root{
		{ID: "flights@gone"},
		{ID: "flights@mirror", Dir: "/cache/one"},
		{ID: "flights@professor", Dir: filepath.Join(market, "plugins", "flights")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Roots = %+v, want %+v", got, want)
	}
}

// A record that exists but cannot be parsed is "could not look", never "not
// enabled"; no enabled key is found nothing.
func TestRootsFailureNeverReadsAsAbsence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, file, content, wantErr string
	}{
		{name: "settings unparsable", file: "settings.json", content: "{", wantErr: "unexpected end of JSON input"},
		{name: "nothing enabled", file: "settings.json", content: `{"enabledPlugins":{"flights@professor":false}}`},
		{
			name: "enabledPlugins no object", file: "settings.json",
			content: `{"enabledPlugins":["flights@professor"]}`, wantErr: "enabledPlugins is no object",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := t.TempDir()
			writeFixture(t, filepath.Join(config, test.file), test.content)
			got, err := Roots(config, "flights")
			want := ""
			if test.wantErr != "" {
				want = "parse " + filepath.Join(config, "settings.json") + ": " + test.wantErr
			}
			if (err == nil) != (want == "") || (err != nil && err.Error() != want) || got != nil {
				t.Fatalf("Roots = %+v, %v; want nil, %q", got, err, want)
			}
		})
	}
}

// AgentFile takes agents/{name}.md unless its frontmatter names another
// agent, then the file whose frontmatter names it.
func TestAgentFileMatchesTheFrontmatterName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "agents", "foreman.md"), "---\nname: foreman\n---\nA\n")
	writeFixture(t, filepath.Join(root, "agents", "plain.md"), "no frontmatter\n")
	writeFixture(t, filepath.Join(root, "agents", "lander.md"), "---\nname: sub-lander\n---\nB\n")
	writeFixture(t, filepath.Join(root, "agents", "land.md"), "---\nname: lander\n---\nC\n")
	for _, test := range []struct {
		agent, want string
		found       bool
	}{
		{agent: "foreman", want: filepath.Join(root, "agents", "foreman.md"), found: true},
		{agent: "plain", want: filepath.Join(root, "agents", "plain.md"), found: true},
		{agent: "lander", want: filepath.Join(root, "agents", "land.md"), found: true},
		{agent: "sub-lander", want: filepath.Join(root, "agents", "lander.md"), found: true},
		{agent: "hunter"},
	} {
		path, found, err := AgentFile(root, test.agent)
		if err != nil || path != test.want || found != test.found {
			t.Errorf("AgentFile(%q) = %q, %t, %v; want %q, %t", test.agent, path, found, err, test.want, test.found)
		}
	}
}
