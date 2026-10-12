package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printFixture is one pfm-launched Claude seat as the fingerprint reads it: a
// pane-root engine in a /proc root, a tmux dir, a crumb dir and a transcript.
type printFixture struct {
	t        *testing.T
	root     string
	procRoot string
	tmuxDir  string
	sidDir   string
	chat     headless.Chat
}

func newPrintFixture(t *testing.T) *printFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &printFixture{
		t: t, root: root,
		procRoot: filepath.Join(root, "proc"), tmuxDir: filepath.Join(root, "tmux"), sidDir: filepath.Join(root, "sid"),
	}
	transcript := filepath.Join(root, "projects", "-work", "sid-1.jsonl")
	for _, directory := range []string{fixture.tmuxDir, fixture.sidDir, filepath.Dir(transcript)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture.write(filepath.Join(fixture.tmuxDir, "cc-1-1-1"), "")
	fixture.write(filepath.Join(fixture.sidDir, "cc-1-1-1.%0"), transcript)
	fixture.write(transcript, `{"type":"user"}`+"\n")
	fixture.engine(41, 100, "claude", "--resume", "sid-1")
	fixture.chat = headless.Chat{
		Name: "lad-a", ID: "sid-1", Engine: pfmengine.Claude, Path: transcript,
		Socket: "cc-1-1-1", Pane: "%0", Live: true, PanePID: 41,
	}
	return fixture
}

func (fixture *printFixture) write(path, content string) {
	fixture.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		fixture.t.Fatal(err)
	}
}

// engine lays pid down in the /proc root, started at tick start with argv.
func (fixture *printFixture) engine(pid int, start uint64, argv ...string) {
	fixture.t.Helper()
	directory := filepath.Join(fixture.procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	cmdline := ""
	for _, argument := range argv {
		cmdline += argument + "\x00"
	}
	fixture.write(filepath.Join(directory, "cmdline"), cmdline)
	fixture.write(filepath.Join(directory, "stat"),
		fmt.Sprintf("%d (%s) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 %d\n", pid, argv[0], start))
}

func (fixture *printFixture) printer() *SeatPrinter {
	return &SeatPrinter{
		tmuxDir: fixture.tmuxDir, sidDir: fixture.sidDir, proc: gather.NewProcFS(fixture.procRoot),
	}
}

func (fixture *printFixture) print() (string, bool) { return fixture.printer().Print(fixture.chat) }

// TestSeatPrintHoldsWhileNothingTheRowRestsOnMoves: a turn written to the
// transcript leaves the fingerprint as it was — the fast path exists for the
// polls between changes — and every input the seat's row rests on moves it.
func TestSeatPrintHoldsWhileNothingTheRowRestsOnMoves(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*printFixture)
		moves  bool
	}{
		{name: "a turn appended to the transcript", change: func(fixture *printFixture) {
			fixture.write(fixture.chat.Path, `{"type":"user"}`+"\n"+`{"type":"assistant"}`+"\n")
		}},
		{name: "a socket born: a respawn", moves: true, change: func(fixture *printFixture) {
			fixture.write(filepath.Join(fixture.tmuxDir, "cc-2-2-2"), "")
		}},
		{name: "the seat's socket gone", moves: true, change: func(fixture *printFixture) {
			if err := os.Remove(filepath.Join(fixture.tmuxDir, "cc-1-1-1")); err != nil {
				fixture.t.Fatal(err)
			}
		}},
		{name: "its crumb rewritten: a /clear", moves: true, change: func(fixture *printFixture) {
			fixture.write(filepath.Join(fixture.sidDir, "cc-1-1-1.%0"), filepath.Join(fixture.root, "next-session.jsonl"))
		}},
		{name: "another seat's crumb born", moves: true, change: func(fixture *printFixture) {
			fixture.write(filepath.Join(fixture.sidDir, "cc-2-2-2"), "/elsewhere.jsonl")
		}},
		{name: "a statusline effort record rewritten beside the crumbs", change: func(fixture *printFixture) {
			fixture.write(filepath.Join(fixture.sidDir, paths.SIDEffortPrefix+"sid-9"), `{"effort":"high"}`)
			fixture.write(filepath.Join(fixture.sidDir, "."+paths.SIDEffortPrefix+"sid-9.tmp-123"), "{")
		}},
		{name: "the engine resuming another session", moves: true, change: func(fixture *printFixture) {
			fixture.engine(41, 100, "claude", "--resume", "sid-2")
		}},
		{name: "a new engine under the same pid", moves: true, change: func(fixture *printFixture) {
			fixture.engine(41, 200, "claude", "--resume", "sid-1")
		}},
		{name: "a transcript born beside its own: a continuation", moves: true, change: func(fixture *printFixture) {
			fixture.write(filepath.Join(filepath.Dir(fixture.chat.Path), "sid-3.jsonl"), "")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPrintFixture(t)
			before, ok := fixture.print()
			if !ok {
				t.Fatal("a pane-root Claude seat was not fingerprinted")
			}
			if again, _ := fixture.print(); again != before {
				t.Fatal("two reads of an unchanged seat differ")
			}
			// A directory's modification time can stay within one clock tick
			// of the first read on a coarse filesystem clock.
			time.Sleep(10 * time.Millisecond)
			test.change(fixture)
			after, ok := fixture.print()
			if !ok {
				t.Fatal("the changed seat was not fingerprinted")
			}
			if moved := after != before; moved != test.moves {
				t.Fatalf("fingerprint moved = %v, want %v", moved, test.moves)
			}
		})
	}
}

// TestSeatPrintRefusesASeatItCannotVouchFor: no fingerprint for a seat the
// fast path could misread — the poll scans instead.
func TestSeatPrintRefusesASeatItCannotVouchFor(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*printFixture)
	}{
		{name: "its engine gone", change: func(fixture *printFixture) {
			if err := os.RemoveAll(filepath.Join(fixture.procRoot, "41")); err != nil {
				fixture.t.Fatal(err)
			}
		}},
		{name: "a shell at the pane root, Claude below it", change: func(fixture *printFixture) {
			fixture.engine(41, 100, "zsh")
		}},
		{name: "its transcript directory gone", change: func(fixture *printFixture) {
			if err := os.RemoveAll(filepath.Dir(fixture.chat.Path)); err != nil {
				fixture.t.Fatal(err)
			}
		}},
		{name: "its tmux directory unreadable", change: func(fixture *printFixture) {
			if err := os.RemoveAll(fixture.tmuxDir); err != nil {
				fixture.t.Fatal(err)
			}
		}},
		{name: "no pane root known", change: func(fixture *printFixture) { fixture.chat.PanePID = 0 }},
		{name: "not live", change: func(fixture *printFixture) { fixture.chat.Live = false }},
		{name: "a Codex seat", change: func(fixture *printFixture) { fixture.chat.Engine = pfmengine.Codex }},
		{name: "no pane", change: func(fixture *printFixture) { fixture.chat.Pane = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPrintFixture(t)
			test.change(fixture)
			if printed, ok := fixture.print(); ok {
				t.Fatalf("Print = %q, ok; want no fingerprint", printed)
			}
		})
	}
}
