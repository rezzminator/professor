package report

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/cmdparse"
)

// workDir is an absolute, symlink-free cwd holding the named files.
func workDir(t *testing.T, files ...string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("line\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// rejectingPython answers every snippet with a parser error.
type rejectingPython struct{}

func (rejectingPython) Analyze(_ context.Context, snippets []cmdparse.Snippet) ([]cmdparse.PyResult, error) {
	out := make([]cmdparse.PyResult, len(snippets))
	for i, s := range snippets {
		out[i] = cmdparse.PyResult{ID: s.ID, Error: "invalid syntax"}
	}
	return out, nil
}

func TestEnsureParsedCachesPartsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "a.txt")
	seed(t, store, bash("b1", "s", "", ms(time.Hour), dir, `cat a.txt && python3 -c "open('a.txt')"`, 10))
	seed(t, store, bash("b2", "s", "", ms(time.Hour), "rel/dir", "ls", 10))
	missing := cmdparse.Python3{Program: "python3-not-installed-for-this-test"}
	got, err := EnsureParsed(ctx, store, "", missing)
	if err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	if got.Parsed != 1 || got.SkippedRelative != 1 {
		t.Errorf("first run = %+v, want 1 parsed, 1 skipped for a relative cwd", got)
	}
	var files string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT files FROM command_parts WHERE tool_use_id = 'b1' AND seq = 0").Scan(&files); err != nil {
		t.Fatalf("read cached part: %v", err)
	}
	if !strings.Contains(files, `read-whole\t\t1\t`+filepath.Join(dir, "a.txt")) {
		t.Errorf("cached files = %s, want the encoded read-whole of a.txt", files)
	}
	again, err := EnsureParsed(ctx, store, "", missing)
	if err != nil {
		t.Fatalf("EnsureParsed again: %v", err)
	}
	if again.Parsed != 0 {
		t.Errorf("second run parsed %d calls, want 0", again.Parsed)
	}
	table, err := Commands(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	notes := strings.Join(table.Notes, "\n")
	for _, want := range []string{"1 snippets unparsed: python-unavailable", "1 Bash calls skipped by the parser"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
}

func TestEnsureParsedRecordsParseFault(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	seed(t, store, bash("b1", "s", "", ms(time.Hour), workDir(t), `python3 -c "def ("`, 10))
	if _, err := EnsureParsed(ctx, store, "", rejectingPython{}); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	var stage, message string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT stage, error FROM faults WHERE tool_use_id = 'b1'").Scan(&stage, &message); err != nil {
		t.Fatalf("read parse fault: %v", err)
	}
	if stage != "parse" || !strings.Contains(message, "invalid syntax") {
		t.Errorf("fault = %s %q, want a parse fault carrying the message", stage, message)
	}
}

func TestFileRefRoundTrip(t *testing.T) {
	for _, ref := range []cmdparse.FileRef{
		{Path: "/w/a b.txt", Action: cmdparse.ActionReadRange, Range: "1,20", Exists: true},
		{Path: "/w/gone\tscratch.txt", Action: cmdparse.ActionWrite},
	} {
		got, err := DecodeFileRef(EncodeFileRef(ref))
		if err != nil || got != ref {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, ref)
		}
	}
	// The three-field form predates exists; a stored value of any other shape
	// is an error naming it, never a guess.
	for _, stored := range []string{"no tabs", "read-whole\t\t/w/a.txt", "read-whole\t\tyes\t/w/a.txt"} {
		if _, err := DecodeFileRef(stored); err == nil || !strings.Contains(err.Error(), strconv.Quote(stored)) {
			t.Errorf("DecodeFileRef(%q) error = %v, want one naming the stored value", stored, err)
		}
	}
}

func TestEnsureParsedStoresConditional(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "a.txt")
	seed(t, store, bash("b1", "s", "", ms(time.Hour), dir, `test -f a.txt && cat a.txt`, 10))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	for seq, want := range []int64{0, 1} {
		var got int64
		if err := store.DB().QueryRowContext(ctx,
			"SELECT conditional FROM command_parts WHERE tool_use_id = 'b1' AND seq = ?", seq).Scan(&got); err != nil {
			t.Fatalf("read part %d: %v", seq, err)
		}
		if got != want {
			t.Errorf("part %d conditional = %d, want %d", seq, got, want)
		}
	}
}

// TestEnsureParsedReparsesAnOlderParsersParts: parts cached by an older
// parser are parsed again, and that parse's faults replace the old ones, so a
// parser fix reaches every call still in the window.
func TestEnsureParsedReparsesAnOlderParsersParts(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "a.txt")
	seed(t, store, bash("b1", "s", "", ms(time.Hour), dir, "cat a.txt", 10))
	stale := []callmeter.CommandPart{
		{Seq: 0, Lang: cmdparse.LangSh, ParseStatus: cmdparse.StatusError, Parser: cmdparse.Version - 1},
	}
	if err := store.ReplaceCommandParts(ctx, "b1", stale); err != nil {
		t.Fatalf("seed stale parts: %v", err)
	}
	if err := store.AddFault(ctx, callmeter.Fault{
		TS: ms(time.Hour), SessionID: "s", ToolUseID: "b1", Stage: callmeter.StageParse, Error: "stale parse",
	}); err != nil {
		t.Fatalf("seed stale fault: %v", err)
	}
	got, err := EnsureParsed(ctx, store, "", nil)
	if err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	if got.Parsed != 1 {
		t.Errorf("parsed %d calls, want the stale call parsed again", got.Parsed)
	}
	var status string
	var parser int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT parse_status, parser FROM command_parts WHERE tool_use_id = 'b1' AND seq = 0").
		Scan(&status, &parser); err != nil {
		t.Fatalf("read part: %v", err)
	}
	if status != cmdparse.StatusOK || parser != cmdparse.Version {
		t.Errorf("part = (%s, parser %d), want (ok, parser %d)", status, parser, cmdparse.Version)
	}
	var faults int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM faults WHERE tool_use_id = 'b1' AND stage = ?", callmeter.StageParse).
		Scan(&faults); err != nil {
		t.Fatalf("count faults: %v", err)
	}
	if faults != 0 {
		t.Errorf("%d parse faults left from the stale parse, want 0", faults)
	}
}
