package installer

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// engineSyncTemplate is a clone's templates/global/engine-sync.json: the
// machine policy, its first key the marker that makes the written file pfm's.
const engineSyncTemplate = "{\n  \"version\": 1,\n  \"machine\": {\n    \"managedBy\": \"professor\",\n" +
	"    \"retiredAgents\": [\"old-role\"]\n  }\n}\n"

// engineSyncConfigEngine is engineSyncGlobalEngine with the clone's template
// written and no XDG_CONFIG_HOME, so the old config directory is home/.config.
func engineSyncConfigEngine(t *testing.T, mode Mode, roster []string) (*engine, *bytes.Buffer, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	installer, output, home := engineSyncGlobalEngine(t, mode, roster)
	writeFixture(t, engineSyncPolicySource(installer.options.SourceRepo), engineSyncTemplate)
	return installer, output, home
}

// jsonList renders items as the global file lays a string list out under "targets".
func jsonList(items ...string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "[\n      \"" + strings.Join(items, "\",\n      \"") + "\"\n    ]"
}

// wantEngineSyncFile is the template with the roster spliced in as its last key.
func wantEngineSyncFile(homes []string, claudeDir, repo string, capabilities []string) string {
	return "{\n  \"version\": 1,\n  \"machine\": {\n    \"managedBy\": \"professor\",\n" +
		"    \"retiredAgents\": [\"old-role\"]\n  },\n" +
		"  \"targets\": {\n    \"codexHomes\": " + jsonList(homes...) + ",\n" +
		"    \"claudeConfigDirs\": " + jsonList(claudeDir) + ",\n" +
		"    \"sourceRepo\": \"" + repo + "\",\n" +
		"    \"capabilities\": " + jsonList(capabilities...) + "\n  }\n}\n"
}

// The global file names this install's roster, an empty one as an empty list,
// is left alone when unchanged, rewritten when the roster moved, and retired
// by uninstall.
func TestWireEngineSyncConfigWritesThenRetires(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		roster       []string
		gptAgents    bool
		capabilities []string
	}{
		{name: "one Codex home"},
		{name: "the empty roster", roster: []string{}},
		{name: "gpt agents on", gptAgents: true, capabilities: []string{"gpt-agents"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			installer, output, home := engineSyncConfigEngine(t, ModeApply, testCase.roster)
			installer.options.GptAgents = testCase.gptAgents
			path := enginesync.GlobalConfigPath(home)
			if err := installer.wireEngineSyncConfig(); err != nil {
				t.Fatal(err)
			}
			homes := []string{filepath.Join(home, ".codex")}
			if testCase.roster != nil {
				homes = nil
			}
			want := wantEngineSyncFile(homes, filepath.Join(home, ".claude"),
				filepath.Join(home, "blueprint"), testCase.capabilities)
			if got := readFixture(t, path); got != want {
				t.Fatalf("global file:\n%s\nwant:\n%s", got, want)
			}
			if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("global file mode = %v (%v), want a regular 0600 file", info, err)
			}
			if !strings.Contains(output.String(), "  change  write "+path+"\n") {
				t.Fatalf("the write was not reported:\n%s", output.String())
			}
			output.Reset()
			if err := installer.wireEngineSyncConfig(); err != nil {
				t.Fatal(err)
			}
			if output.String() != "  ok      "+path+"\n" {
				t.Fatalf("an unchanged global file was rewritten:\n%s", output.String())
			}
			installer.options.Mode = ModeUninstall
			if err := installer.retireEngineSyncFile(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("uninstall left %s: %v", path, err)
			}
		})
	}
}

func TestWireEngineSyncConfigRewritesAStaleRoster(t *testing.T) {
	installer, output, home := engineSyncConfigEngine(t, ModeApply, nil)
	path := enginesync.GlobalConfigPath(home)
	writeFixture(t, path, `{"version":1,"machine":{"managedBy":"professor"},"targets":{"codexHomes":["/gone"]}}`)
	if err := installer.wireEngineSyncConfig(); err != nil {
		t.Fatal(err)
	}
	want := wantEngineSyncFile([]string{filepath.Join(home, ".codex")}, filepath.Join(home, ".claude"),
		filepath.Join(home, "blueprint"), nil)
	if got := readFixture(t, path); got != want {
		t.Fatalf("global file:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(output.String(), "  change  write "+path+"\n") {
		t.Fatalf("the rewrite was not reported:\n%s", output.String())
	}
}

// A file at the global path that is not pfm's — no marker, another owner's,
// unparsable, a link, a FIFO (never read) — stays byte-identical, and so does
// it under uninstall.
func TestWireEngineSyncConfigLeavesAnUnownedFile(t *testing.T) {
	for _, testCase := range []struct{ name, content string }{
		{name: "no marker", content: `{"version":1}`},
		{name: "another owner", content: `{"version":1,"machine":{"managedBy":"operator"}}`},
		{name: "unparsable", content: "not json"},
		{name: "a link to an owned-looking file"},
		{name: "a FIFO"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			installer, output, home := engineSyncConfigEngine(t, ModeApply, nil)
			path := enginesync.GlobalConfigPath(home)
			switch testCase.name {
			case "a link to an owned-looking file":
				owned := filepath.Join(home, "elsewhere.json")
				writeFixture(t, owned, `{"version":1,"machine":{"managedBy":"professor"}}`)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(owned, path); err != nil {
					t.Fatal(err)
				}
			case "a FIFO":
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				writeFixture(t, path, testCase.content)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := installer.wireEngineSyncConfig(); err != nil {
				t.Fatal(err)
			}
			installer.options.Mode = ModeUninstall
			if err := installer.retireEngineSyncFile(); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("%s was replaced: %v -> %v (%v)", path, before.Mode(), after, err)
			}
			if before.Mode().IsRegular() && readFixture(t, path) != testCase.content {
				t.Fatalf("%s was rewritten:\n%s", path, readFixture(t, path))
			}
			want := "  skip    " + path + " is not managed by professor — left unchanged " +
				"(set machine.managedBy to \"professor\" to let pfm write it)\n"
			if !strings.Contains(output.String(), want) {
				t.Fatalf("transcript lacks the skip line %q:\n%s", want, output.String())
			}
		})
	}
}

func TestWireEngineSyncConfigWithoutTheTemplate(t *testing.T) {
	installer, output, home := engineSyncGlobalEngine(t, ModeApply, nil)
	want := "  skip    engine-sync machine policy source missing: " + engineSyncPolicySource(
		installer.options.SourceRepo,
	)
	if err := installer.wireEngineSyncConfig(); err != nil {
		t.Fatalf("optional policy blocked install: %v; transcript: %s", err, output.String())
	}
	if installer.report.Skipped != 1 || !strings.Contains(output.String(), want+"\n") {
		t.Fatalf("missing policy must be one counted, visible skip: report=%+v; transcript: %s",
			installer.report, output.String())
	}
	if _, err := os.Lstat(enginesync.GlobalConfigPath(home)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a global file was written without a template: %v", err)
	}
}

// An uninstall retires the global file when it is pfm's; an operator's global
// file stays byte-identical.
func TestUninstallRetiresTheGlobalFile(t *testing.T) {
	for _, owned := range []bool{true, false} {
		name := "an operator's file"
		if owned {
			name = "pfm's file"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", "")
			home := t.TempDir()
			clone := filepath.Join(home, ".professor")
			writeFixture(t, engineSyncPolicySource(clone), engineSyncTemplate)
			_, engineSync := testjail.FakeEngineSyncRunner(t, `{"ok":true,"events":[],"error":""}`, 0)
			path := enginesync.GlobalConfigPath(home)
			if owned {
				writeFixture(t, path, wantEngineSyncFile(nil, filepath.Join(home, ".claude"), clone, nil))
			} else {
				writeFixture(t, path, `{"version":1}`)
			}
			var output bytes.Buffer
			if _, err := Run(context.Background(), Options{
				EngineSyncRunner: engineSync,
				MCPConfigPath:    testConfigPath(t), SourceRepo: clone,
				Mode: ModeUninstall, Home: home, Stdout: &output, Runner: &fakeRunner{},
			}); err != nil {
				t.Fatalf("uninstall: %v\n%s", err, output.String())
			}
			_, err := os.Lstat(path)
			if owned != errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("global file after uninstall: %v (owned=%v)", err, owned)
			}
			if !owned && readFixture(t, path) != `{"version":1}` {
				t.Fatalf("uninstall rewrote the operator's file: %s", readFixture(t, path))
			}
		})
	}
}

// A dry run does not preview `engine-sync global` when the install would write
// the global file: it prints one named skip line and makes no engine-sync
// call, since engine-sync reads the file first.
func TestEngineSyncPreviewIsSkippedByNameWhenTheInstallChangesTheConfig(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		mode  Mode
		setup func(t *testing.T, installer *engine, home string)
		want  string
	}{
		{
			name: "the global file is absent", mode: ModeDryRun,
			setup: func(*testing.T, *engine, string) {},
			want:  "would write {file}",
		},
		{
			name: "pfm's file has a stale roster", mode: ModeDryRun,
			setup: func(t *testing.T, _ *engine, home string) {
				writeFixture(t, enginesync.GlobalConfigPath(home),
					`{"version":1,"machine":{"managedBy":"professor"},"targets":{"codexHomes":["/gone"]}}`)
			},
			want: "would write {file}",
		},
		{
			name: "a rule link this pass retires", mode: ModeDryRun,
			setup: func(t *testing.T, installer *engine, home string) {
				writeCurrentGlobalFile(t, installer, home)
				installer.markRemoved(filepath.Join(installer.options.ConfigDir, "rules", "renamed.md"))
			},
			want: "would retire {rule}",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			installer, output, home := engineSyncConfigEngine(t, testCase.mode, nil)
			installer.apply = false
			testCase.setup(t, installer, home)
			record, engineSync := testjail.FakeEngineSyncRunner(t, `{"ok":true,"events":[],"error":""}`, 0)
			installer.options.EngineSyncRunner = engineSync
			if err := installer.wireEngineSyncGlobal(nil); err != nil {
				t.Fatal(err)
			}
			want := strings.NewReplacer("{file}", enginesync.GlobalConfigPath(home),
				"{rule}", filepath.Join(installer.options.ConfigDir, "rules", "renamed.md")).Replace(testCase.want)
			line := "  skip    engine-sync global preview skipped: this install " + want +
				", which engine-sync reads before it plans; the apply runs it after\n"
			if output.String() != line {
				t.Fatalf("transcript:\n%s\nwant:\n%s", output.String(), line)
			}
			if _, err := os.Lstat(record); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("engine-sync ran during the skipped preview: %v", err)
			}
			if len(installer.planErrors) != 0 {
				t.Fatalf("planErrors = %v, want none", installer.planErrors)
			}
		})
	}
}

// writeCurrentGlobalFile writes the file an apply of installer would leave.
func writeCurrentGlobalFile(t *testing.T, installer *engine, home string) {
	t.Helper()
	apply := *installer
	apply.apply, apply.options.Mode, apply.options.Stdout = true, ModeApply, &bytes.Buffer{}
	if err := apply.wireEngineSyncConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(enginesync.GlobalConfigPath(home)); err != nil {
		t.Fatal(err)
	}
}

// A dry run that changes neither runs engine-sync against the real home, over
// pfm's current file or an operator's.
func TestEngineSyncPreviewWithoutAChangeUsesTheRealHome(t *testing.T) {
	for _, name := range []string{"pfm's file already current", "an operator's file"} {
		t.Run(name, func(t *testing.T) {
			installer, output, home := engineSyncConfigEngine(t, ModeDryRun, nil)
			installer.apply = false
			content := `{"version":1,"operator":true}`
			if name == "pfm's file already current" {
				writeCurrentGlobalFile(t, installer, home)
				content = readFixture(t, enginesync.GlobalConfigPath(home))
			} else {
				writeFixture(t, enginesync.GlobalConfigPath(home), content)
			}
			record, engineSync := testjail.FakeEngineSyncRunner(t, `{"ok":true,"events":[],"error":""}`, 0)
			installer.options.EngineSyncRunner = engineSync
			if err := installer.wireEngineSyncGlobal(nil); err != nil {
				t.Fatalf("%v\n%s", err, output.String())
			}
			calls := testjail.EngineSyncCalls(t, record)
			if len(calls) != 1 || !strings.HasPrefix(calls[0], "global --home "+home+" ") ||
				!strings.Contains(calls[0], " --plan") {
				t.Fatalf("engine-sync calls = %q, want one --plan over the real home %s", calls, home)
			}
			if got := readFixture(t, record+".config"); got != content {
				t.Fatalf("engine-sync saw %q, want %q", got, content)
			}
		})
	}
}
