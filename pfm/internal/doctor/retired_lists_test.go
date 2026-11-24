package doctor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// A clone whose retired lists cannot be read is one FAIL row naming the file,
// and the checks needing a list run on empty ones; a readable file prints nothing.
func TestLoadRetiredLists(t *testing.T) {
	for _, testCase := range []struct {
		name, raw string
		failures  int
		want      retired.Lists
	}{
		{name: "readable", raw: `{"units":["old.service"]}`, want: retired.Lists{Units: []string{"old.service"}}},
		{name: "unparseable", raw: `{"units":`, failures: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			file := filepath.Join(clone, filepath.FromSlash(retired.FileRel))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(testCase.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			lists, failures := loadRetiredLists(&stdout, home)
			if failures != testCase.failures || !reflect.DeepEqual(lists, testCase.want) {
				t.Fatalf(
					"loadRetiredLists() = %+v, %d; want %+v, %d",
					lists,
					failures,
					testCase.want,
					testCase.failures,
				)
			}
			wantPrefix := ""
			if testCase.failures != 0 {
				wantPrefix = "doctor: retired_lists FAIL retired lists " + file + ": decode: "
			}
			if got := stdout.String(); wantPrefix == "" && got != "" || !strings.HasPrefix(got, wantPrefix) ||
				wantPrefix != "" && !strings.HasSuffix(got, " — restore "+retired.FileRel+" in the clone\n") {
				t.Fatalf("stdout = %q, want a row starting %q", got, wantPrefix)
			}
		})
	}
}

// A plugin retired with no successor still installed in the store is one
// warning naming every trace and pfm install as its repair; a clean store and
// an empty list print nothing; an unreadable record is one failure.
func TestPrintRetiredClaudePluginsDoctor(t *testing.T) {
	for _, testCase := range []struct {
		name, installs, want string
		ids                  []string
		plugins              []pfmconfig.ClaudePlugin
		// sourceErr is a checkout host whose source clone, and so its marketplace list, is unread.
		sourceErr          string
		settingsDir        bool
		warnings, failures int
	}{
		{
			name:     "installed",
			installs: `{"version":2,"plugins":{"tinsel@professor":[{"scope":"user","installPath":"{store}/plugins/cache/professor/tinsel/1.0"}]}}`,
			ids:      []string{"tinsel@professor"},
			want: "doctor: claude_plugins plugin tinsel@professor is retired with no successor but still on this host: " +
				"plugins in {store}/plugins/installed_plugins.json — run pfm install --yes removes every trace of it\n",
			warnings: 1,
		},
		{
			name:     "marketplace list unread",
			installs: `{"version":2,"plugins":{"tinsel@professor":[{"scope":"user","installPath":"{store}/plugins/cache/professor/tinsel/1.0"}]}}`,
			ids:      []string{"tinsel@professor"}, sourceErr: "no clone marker",
			want: "doctor: claude_plugins plugin tinsel@professor is retired with no successor but still on this host: " +
				"plugins in {store}/plugins/installed_plugins.json — pfm install --yes removes no retired plugin while " +
				"the marketplace plugin list is unread (marketplace plugin list unread: source clone unresolved: no clone marker); " +
				"repair that first, then run pfm install --yes\n",
			warnings: 1,
		},
		{
			name:     "still planned",
			installs: `{"version":2,"plugins":{"tinsel@professor":[{"scope":"user","installPath":"{store}/plugins/cache/professor/tinsel/1.0"}]}}`,
			ids:      []string{"tinsel@professor"}, plugins: []pfmconfig.ClaudePlugin{{ID: "tinsel"}},
			want: "doctor: claude_plugins plugin tinsel@professor is retired with no successor but claude.plugins or the " +
				"source clone's marketplace still plans it — drop it there or from claudePlugins[\"(none)\"] in the retired lists\n",
			warnings: 1,
		},
		{
			name: "settings unreadable", installs: `{"version":2,"plugins":{}}`, ids: []string{"tinsel@professor"},
			settingsDir: true,
			want:        "doctor: claude_plugins could not look for retired plugins: read {store}/settings.json: is a directory\n",
			failures:    1,
		},
		{name: "clean", installs: `{"version":2,"plugins":{}}`, ids: []string{"tinsel@professor"}},
		{name: "nothing retired", installs: `{"plugins":`},
		{
			name: "unreadable", installs: `{"plugins":`, ids: []string{"tinsel@professor"},
			want: "doctor: claude_plugins could not look for retired plugins: parse " +
				"{store}/plugins/installed_plugins.json: unexpected EOF\n",
			failures: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			store := filepath.Join(home, ".claude")
			fill := strings.NewReplacer("{store}", store)
			file := filepath.Join(store, "plugins", "installed_plugins.json")
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(fill.Replace(testCase.installs)), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.settingsDir {
				if err := os.Mkdir(filepath.Join(store, "settings.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			tally := &doctorTally{}
			build := installer.ClaudePluginBuild{Home: home, Plugins: testCase.plugins}
			if testCase.sourceErr != "" {
				build.FromCheckout, build.SourceRepoErr = true, errors.New(testCase.sourceErr)
			}
			printRetiredClaudePluginsDoctor(&stdout, home, nil, testCase.ids, build, tally)
			want := fill.Replace(testCase.want)
			if got := stdout.String(); got != want || tally.warnings != testCase.warnings ||
				tally.failures != testCase.failures {
				t.Fatalf("output %q, warnings %d, failures %d\nwant %q, %d, %d (record %s)",
					got, tally.warnings, tally.failures, want, testCase.warnings, testCase.failures, file)
			}
		})
	}
}
