package workbench

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func readArgv(t *testing.T, record string) string {
	t.Helper()
	calls := testjail.EngineSyncCalls(t, record)
	if len(calls) != 1 {
		t.Fatalf("engine-sync calls = %q, want one", calls)
	}
	return calls[0]
}

const cleanMirrorReply = `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],` +
	`"wrote":1,"unchanged":0,"deleted":0,"error":""}`

// skippedMirrorReply is engine-sync's --json answer for an engine
// ENGINE_SYNC_DISABLE switches off.
func skippedMirrorReply(engine pfmengine.ID) string {
	return `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,"deleted":0,` +
		`"error":"","skipped":["` + pfmengine.MustLookup(engine).LongName + `"]}`
}

func TestEnsureMirror(t *testing.T) {
	for _, testCase := range []struct {
		engine   pfmengine.ID
		argv     string
		settings bool
		// disable is the managed root's settings ENGINE_SYNC_DISABLE; the
		// mirror replies with engine-sync's skip.
		disable string
	}{
		{engine: pfmengine.Codex, argv: "codex build"},
		{engine: pfmengine.OpenCode, argv: "opencode build"},
		{engine: pfmengine.Claude},
		{engine: pfmengine.Codex, settings: true},
		{engine: pfmengine.OpenCode, settings: true},
		{engine: pfmengine.Codex, argv: "codex build", disable: "codex"},
		{engine: pfmengine.OpenCode, argv: "opencode build", disable: "codex,opencode"},
	} {
		t.Run(
			string(testCase.engine)+map[bool]string{true: "/settings persona"}[testCase.settings],
			func(t *testing.T) {
				root, dir := benchFixture(t)
				bench, home := LoadBench(dir, root), t.TempDir()
				if testCase.settings {
					bench = Persona{Prompt: "team.md"}.Bench // a settings persona carries no bench
				}
				reply := cleanMirrorReply
				if testCase.disable != "" {
					writeBenchFile(t, paths.Settings(root), `{"envs":{"ENGINE_SYNC_DISABLE":"`+testCase.disable+`"}}`)
					reply = skippedMirrorReply(testCase.engine)
				}
				record, runner := testjail.FakeEngineSyncRunner(t, reply, 0)
				if err := EnsureMirror(bench, testCase.engine, home, runner); err != nil {
					t.Fatal(err)
				}
				if testCase.argv == "" {
					if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("%s launch without a bench mirror ran engine-sync: %v", testCase.engine, err)
					}
					return
				}
				if got, want := readArgv(t, record), testCase.argv+" "+dir+" --home "+home+" --json"; got != want {
					t.Fatalf("engine-sync argv = %q, want %q", got, want)
				}
				if got, err := os.ReadFile(record + ".disable"); err != nil || string(got) != testCase.disable+"\n" {
					t.Fatalf("ENGINE_SYNC_DISABLE = %q (%v) in %s.disable, want %q", got, err, record, testCase.disable)
				}
			},
		)
	}
}

func TestEnsureMirrorFails(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		engine pfmengine.ID
		reply  string
		code   int
		want   string
	}{
		{
			name: "problems", engine: pfmengine.Codex, code: 1,
			reply: `{"ok":false,"problems":["INVALID a","CONFLICT b"],"rebuildable":[],"warnings":[],"error":""}`,
			want:  "build the codex mirror of workbench {dir}: INVALID a; CONFLICT b",
		},
		{
			name: "a run failure", engine: pfmengine.OpenCode, code: 1,
			reply: `{"ok":false,"problems":[],"rebuildable":[],"warnings":[],"error":"read config: unexpected EOF"}`,
			want:  "build the opencode mirror of workbench {dir}: engine-sync opencode build {dir}: read config: unexpected EOF",
		},
		{name: "a usage error", engine: pfmengine.Codex, code: 2, want: "build the codex mirror of workbench {dir}: engine-sync codex build {dir} --home {home} --json: exit 2: usage: engine-sync"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			bench, home := LoadBench(dir, root), t.TempDir()
			_, runner := testjail.FakeEngineSyncRunner(t, testCase.reply, testCase.code)
			err := EnsureMirror(bench, testCase.engine, home, runner)
			if got := strings.NewReplacer(dir, "{dir}", home, "{home}").Replace(fmt.Sprint(err)); got != testCase.want {
				t.Fatalf("EnsureMirror = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCheckMirrorProblemSplit(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		engine  pfmengine.ID
		reply   string
		code    int
		want    MirrorCheck
		wantErr string
	}{
		{name: "fresh", engine: pfmengine.Codex, reply: cleanMirrorReply},
		{
			name: "a rebuildable multiset beside a failing line", engine: pfmengine.OpenCode, code: 1,
			reply: `{"ok":false,"problems":["STALE a","CONFLICT b","STALE a"],"rebuildable":["STALE a"],"warnings":[],` +
				`"error":""}`,
			want: MirrorCheck{Rebuildable: []string{"STALE a"}, Failing: []string{"CONFLICT b", "STALE a"}},
		},
		{
			name: "every problem rebuildable", engine: pfmengine.Codex, code: 1,
			reply: `{"ok":false,"problems":["ORPHAN o"],"rebuildable":["ORPHAN o"],"warnings":[],"error":""}`,
			want:  MirrorCheck{Rebuildable: []string{"ORPHAN o"}},
		},
		{name: "a check that could not run", engine: pfmengine.Codex, code: 2, wantErr: "exit 2"},
		{name: "an engine the project disables", engine: pfmengine.OpenCode, reply: skippedMirrorReply(pfmengine.OpenCode)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			bench, home := LoadBench(dir, root), t.TempDir()
			record, runner := testjail.FakeEngineSyncRunner(t, testCase.reply, testCase.code)
			got, err := CheckMirror(bench, testCase.engine, home, runner)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("CheckMirror error = %v, want %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("CheckMirror = %#v, %v; want %#v", got, err, testCase.want)
			}
			engine := map[pfmengine.ID]string{pfmengine.Codex: "codex", pfmengine.OpenCode: "opencode"}[testCase.engine]
			if got, want := readArgv(t, record), engine+" check "+dir+" --home "+home+" --json"; got != want {
				t.Fatalf("engine-sync argv = %q, want %q", got, want)
			}
		})
	}
}
