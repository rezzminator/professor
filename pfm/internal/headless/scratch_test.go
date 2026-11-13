package headless

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// TestPreparedScratchDirReturnsInjectedDirectoryUnchanged pins the pass-
// through branch: every existing test in this package (and every real
// chat-status call carrying options.TempDir) supplies a directory directly,
// and preparedScratchDir must hand it back untouched rather than second-
// guessing it.
func TestPreparedScratchDirReturnsInjectedDirectoryUnchanged(t *testing.T) {
	const injected = "/some/injected/directory"
	got, err := preparedScratchDir(injected)
	if err != nil || got != injected {
		t.Fatalf("preparedScratchDir(%q) = (%q, %v), want (%q, nil)", injected, got, err, injected)
	}
}

// scratchJail mirrors summaryTestStore's jail idiom (paths.EnvHome plus
// paths.EnvSIDDir via t.Setenv) for the tests below that drive
// preparedScratchDir's empty-directory branch: paths.Resolve()'s SIDDir
// defaults to /tmp/cc-sid — the real crumb pfm/CLAUDE.md forbids any test
// from touching — so the override is not optional here.
func scratchJail(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	sidDir := filepath.Join(root, "sid")
	t.Setenv(paths.EnvSIDDir, sidDir)
	return sidDir
}

// chdir moves the process working directory to directory for the rest of the
// test and restores it after, so a test can assert nothing lands relative to
// "wherever the operator happened to be standing."
func chdir(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("chdir %s: %v", directory, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd %s: %v", previous, err)
		}
	})
}

// An empty directory puts a prepared exchange or capture under the jailed SID directory, never a tmp/ in the cwd.
func TestWritePreparedEmptyDirectoryResolvesThroughPathsNotCWD(t *testing.T) {
	prompt := []transcript.Entry{{Role: transcript.RoleUser, Text: "what changed?"}}
	response := []transcript.Entry{{Role: transcript.RoleAssistant, Text: "the cache changed"}}
	for _, row := range []struct {
		name  string
		write func() (string, error)
		want  string
	}{
		{"exchange", func() (string, error) { return writePreparedExchange("", prompt, response, true) }, "{sid}/exchange-N.md"},
		{"capture", func() (string, error) { return writePreparedCapture("", Chat{Name: "seat"}, "live pane content") }, "{sid}/capture-N.md"},
	} {
		t.Run(row.name, func(t *testing.T) {
			sidDir := scratchJail(t)
			cwd := t.TempDir()
			chdir(t, cwd)
			path, err := row.write()
			t.Cleanup(func() { _ = os.Remove(path) })
			_, statErr := os.Stat(filepath.Join(cwd, "tmp"))
			got := regexp.MustCompile(`\d+`).ReplaceAllString(strings.ReplaceAll(path, sidDir, "{sid}"), "N")
			if got != row.want || err != nil || !os.IsNotExist(statErr) {
				t.Fatalf("prepared path = %q, want %q (err %v, cwd tmp/ stat %v)", got, row.want, err, statErr)
			}
		})
	}
}

// TestPreparedScratchDirResolutionFailureIsAnErrorNotAGuess pins that a
// paths.Resolve() failure comes back as an error rather than a silent
// fallback. paths.Resolve() itself refuses to resolve the operator's real
// home while running under `go test` unless PFM_HOME (or the explicit
// PFM_TEST_REAL_HOME opt-out) is set — see paths.go's EnvRealHome comment —
// so clearing both here forces the seam without inventing a new one.
func TestPreparedScratchDirResolutionFailureIsAnErrorNotAGuess(t *testing.T) {
	t.Setenv(paths.EnvHome, "")
	t.Setenv(paths.EnvRealHome, "")
	cwd := t.TempDir()
	chdir(t, cwd)

	dir, err := preparedScratchDir("")
	if err == nil {
		t.Fatalf("preparedScratchDir(empty) = (%q, nil), want a resolution error", dir)
	}
	if err.Error() != "resolve scratch directory: refusing to resolve the operator's real home directory inside a test: point PFM_HOME at a temporary directory (see internal/testjail), or set PFM_TEST_REAL_HOME=1 if this test genuinely must read the host" {
		t.Fatalf("preparedScratchDir error = %q, want it to name the resolve-scratch-directory step", err.Error())
	}

	prompt := []transcript.Entry{{Role: transcript.RoleUser, Text: "what changed?"}}
	response := []transcript.Entry{{Role: transcript.RoleAssistant, Text: "the cache changed"}}
	if path, writeErr := writePreparedExchange("", prompt, response, true); writeErr == nil {
		t.Fatalf("writePreparedExchange(empty directory) = (%q, nil), want the resolution error propagated", path)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(statErr) {
		t.Fatalf(
			"a tmp/ directory appeared in the working directory %s despite the resolution failure: stat err=%v",
			cwd,
			statErr,
		)
	}
}

// A Codex seat writes commentary between the tool calls of one turn, so an
// assistant entry newest is not a finished exchange: only the rollout's
// task_complete or turn_aborted ends the turn. Claude, and a Codex rollout
// holding no turn record, keep the newest-entry rule.
func TestReadLatestExchangeHoldsACodexTurnUntilItsEndRecord(t *testing.T) {
	midTurn := []string{
		codexTurnStartLine,
		codexUserLine,
		codexToolLine,
		codexSaid("checking the next file"),
		codexToolLine,
	}
	commentary := append(append([]string{}, midTurn[:4]...), codexToolLine, codexSaid("still looking"))
	for _, testCase := range []struct {
		name     string
		engine   pfmengine.ID
		lines    []string
		complete bool
	}{
		{
			name:     "codex commentary between tool calls is mid-turn",
			engine:   pfmengine.Codex,
			lines:    commentary,
			complete: false,
		},
		{
			name:     "codex task_complete ends the turn",
			engine:   pfmengine.Codex,
			lines:    append(append([]string{}, commentary...), codexTurnCompleteLine),
			complete: true,
		},
		{
			name:     "codex turn_aborted ends the turn",
			engine:   pfmengine.Codex,
			lines:    append(append([]string{}, commentary...), codexAbortLine),
			complete: true,
		},
		{
			name:     "codex tool call newest stays partial",
			engine:   pfmengine.Codex,
			lines:    midTurn,
			complete: false,
		},
		{
			name:     "codex without turn records keeps the newest-entry rule",
			engine:   pfmengine.Codex,
			lines:    []string{codexUserLine, codexToolLine, codexSaid("answered")},
			complete: true,
		},
		{
			name:     "claude is unchanged",
			engine:   pfmengine.Claude,
			lines:    []string{user("hello"), tool("Read"), assistant("answered")},
			complete: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(testCase.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			exchange, found, err := readLatestExchange(context.Background(), Chat{
				Name:   "seat",
				Engine: testCase.engine,
				Path:   path,
			})
			if err != nil || !found {
				t.Fatalf("readLatestExchange() found=%v err=%v, want the exchange", found, err)
			}
			if exchange.complete != testCase.complete {
				t.Fatalf("readLatestExchange().complete = %v, want %v", exchange.complete, testCase.complete)
			}
		})
	}
}
