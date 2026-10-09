package workbench

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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

func TestEnsureMirror(t *testing.T) {
	for _, testCase := range []struct {
		engine pfmengine.ID
		argv   string
	}{
		{engine: pfmengine.Codex, argv: "codex build"},
		{engine: pfmengine.OpenCode, argv: "opencode build"},
		{engine: pfmengine.Claude},
	} {
		t.Run(string(testCase.engine), func(t *testing.T) {
			root, dir := benchFixture(t)
			bench, home := LoadBench(dir, root), t.TempDir()
			record := testjail.FakeEngineSync(t, cleanMirrorReply, 0)
			if err := EnsureMirror(bench, testCase.engine, home); err != nil {
				t.Fatal(err)
			}
			if testCase.argv == "" {
				if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("a Claude launch ran engine-sync: %v", err)
				}
				return
			}
			if got, want := readArgv(t, record), testCase.argv+" "+dir+" --home "+home+" --json"; got != want {
				t.Fatalf("engine-sync argv = %q, want %q", got, want)
			}
		})
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
			want:  "INVALID a; CONFLICT b",
		},
		{
			name: "a run failure", engine: pfmengine.OpenCode, code: 1,
			reply: `{"ok":false,"problems":[],"rebuildable":[],"warnings":[],"error":"read config: unexpected EOF"}`,
			want:  "read config: unexpected EOF",
		},
		{name: "a usage error", engine: pfmengine.Codex, code: 2, want: "exit 2: usage: engine-sync"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			bench, home := LoadBench(dir, root), t.TempDir()
			testjail.FakeEngineSync(t, testCase.reply, testCase.code)
			prefix := "build the " + pfmengine.MustLookup(
				testCase.engine,
			).LongName + " mirror of workbench " + dir + ": "
			err := EnsureMirror(bench, testCase.engine, home)
			if err == nil || !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("EnsureMirror = %v, want %q then %q", err, prefix, testCase.want)
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
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			bench, home := LoadBench(dir, root), t.TempDir()
			record := testjail.FakeEngineSync(t, testCase.reply, testCase.code)
			got, err := CheckMirror(bench, testCase.engine, home)
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
