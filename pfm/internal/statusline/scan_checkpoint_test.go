package statusline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// scanFixtureLines is a Claude transcript exercising every rule the folds
// carry across a checkpoint: a counted prompt and a junk one, a response
// streamed as two lines (the last counts once per message.id), a response with
// no key, tool calls and an errored result, a sidechain line, a compaction
// after usage, a model switch, cost-state anchors, and a CRLF line.
var scanFixtureLines = []string{
	`{"type":"user","timestamp":"2026-10-29T01:00:00Z","message":{"content":"build the parser"}}`,
	`{"type":"assistant","timestamp":"2026-10-29T01:00:05Z","requestId":"r1","message":{"id":"m1",` +
		`"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"output_tokens":1}}}`,
	`{"type":"assistant","timestamp":"2026-10-29T01:00:06Z","requestId":"r1","message":{"id":"m1",` +
		`"model":"claude-opus-5-5","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"cache_read_input_tokens":9000,"cache_creation_input_tokens":1000,` +
		`"output_tokens":50}}}`,
	`{"type":"user","timestamp":"2026-10-29T01:00:07Z","message":{"content":[{"type":"tool_result",` +
		`"tool_use_id":"t1","is_error":true}]}}`,
	`{"type":"assistant","isSidechain":true,"message":{"id":"side","model":"claude-sonnet-5-5",` +
		`"usage":{"input_tokens":99999,"output_tokens":9}}}`,
	`{"type":"assistant","timestamp":"2026-10-29T01:00:09Z","message":{"model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":5,"output_tokens":5}}}`,
	`{"type":"cost-state","totalCostUSD":1.25,"startTime":1793235600000,"totalDuration":9000}`,
	`{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-29T01:10:00Z",` +
		`"compactMetadata":{"postTokens":4200}}`,
	`{"type":"user","timestamp":"2026-10-29T01:10:01Z","message":{"content":"<command-name>/clear</command-name>"}}`,
	`{"type":"user","timestamp":"2026-10-29T01:10:02Z","message":{"content":"now the lexer"}}` + "\r",
	`{"type":"assistant","timestamp":"2026-10-29T01:10:05Z","requestId":"r2","effort":"high","message":{"id":"m2",` +
		`"model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],` +
		`"usage":{"input_tokens":20,"cache_read_input_tokens":3000,"output_tokens":70}}}`,
	`not json at all`,
	`{"type":"assistant","timestamp":"2026-10-29T01:10:09Z","requestId":"r3","message":{"id":"m3",` +
		`"model":"claude-nonesuch-9","content":[{"type":"tool_use","id":"t2","name":"Agent"}],` +
		`"usage":{"input_tokens":7,"output_tokens":3}}}`,
}

// foldKinds are the three folds a checkpoint carries, each with what a render
// takes from it: fold builds an empty one, view the facts compared.
var foldKinds = []struct {
	kind string
	fold func(t *testing.T) foldState
	view func(state foldState) string
}{
	{
		kind: "gauge-claude",
		fold: func(t *testing.T) foldState {
			return newGaugeFold(string(pfmengine.Claude), newSpendScan(testPrices(t)))
		},
		view: func(state foldState) string {
			fold := state.(*gaugeFold)
			return fmt.Sprintf("meta=%+v used=%v hit=%d spend=%s",
				fold.Meta.Meta, fold.Meta.ClaudeUsageSeen, fold.CacheHit, spendView(fold.Spend.total()))
		},
	},
	{
		kind: "spend",
		fold: func(t *testing.T) foldState { return newSpendScan(testPrices(t)) },
		view: func(state foldState) string { return spendView(state.(*spendScan).total()) },
	},
	{
		kind: "activity",
		fold: func(t *testing.T) foldState { return newActivityFold(testPrices(t)) },
		view: func(state foldState) string {
			fold := state.(*activityFold)
			return fmt.Sprintf("seen=%d last=%s errors=%d compactions=%d hit=%d context=%d effort=%s model=%s "+
				"turn=%v/%v/%v/%s spend=%s", len(fold.Seen), fold.Last.UTC(), fold.Errors, fold.Compactions,
				fold.CacheHit, fold.Context, fold.Effort, fold.Model, fold.Turn.Open, fold.Turn.Delegating,
				fold.Turn.Seen, fold.Turn.Last.UTC(), spendView(fold.Spend.total()))
		},
	},
}

// spendView is a spend as a render shows it, the dollars to the micro-dollar
// (a sum over a map adds in no fixed order).
func spendView(spend agentSpend) string {
	states := make([]string, 0, len(spend.costStates))
	for _, state := range spend.costStates {
		states = append(states, fmt.Sprintf("%v@%s..%s", state.USD, state.At.UTC(), state.Until.UTC()))
	}
	return fmt.Sprintf("usd=%.6f in=%d out=%d tools=%d priced=%d partial=%v states=%v",
		spend.usd, spend.in, spend.out, spend.tools, spend.priced, spend.partial, states)
}

// writeInPlace replaces path's content in place, keeping its inode, the way an
// append grows it.
func writeInPlace(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte(body), 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// fullView is the view of one fold of path with no checkpoint.
func fullView(t *testing.T, kind string, fold func(*testing.T) foldState, view func(foldState) string,
	path string,
) string {
	t.Helper()
	state := fold(t)
	done, err := transcriptScans{}.fold(path, kind, state)
	if err != nil || !done {
		t.Fatalf("full %s fold: done %v, %v", kind, done, err)
	}
	return view(state)
}

// TestFoldResumedAtAnySplitEqualsOneFullRead writes the fixture in two and in
// three increments, cut at every line boundary and inside every line (one
// byte in, its middle, one byte short of its newline, so a torn final line
// meets the fold; the second cut one or two splits after the first), folds
// after each increment from the checkpoint the previous fold kept, and holds
// every fold's view to a full read of the same bytes: Meta, cache hit, spend,
// activity.
func TestFoldResumedAtAnySplitEqualsOneFullRead(t *testing.T) {
	for _, ending := range []string{"\n", ""} {
		body := strings.Join(scanFixtureLines, "\n") + ending
		boundaries := []int{0}
		var splits []int
		for index := range body {
			if body[index] != '\n' {
				continue
			}
			from := boundaries[len(boundaries)-1]
			splits = append(splits, from, from+1, (from+index)/2, index)
			boundaries = append(boundaries, index+1)
		}
		splits = append(splits, boundaries[len(boundaries)-1], len(body)-1)
		for _, kind := range foldKinds {
			t.Run(fmt.Sprintf("%s/ending%q", kind.kind, ending), func(t *testing.T) {
				for index, cut := range splits {
					checkIncrements(t, kind.kind, kind.fold, kind.view, body, []int{cut, len(body)})
					// A third increment cut further on: the next split, and the one after it.
					for _, next := range splits[index+1 : min(index+3, len(splits))] {
						checkIncrements(t, kind.kind, kind.fold, kind.view, body, []int{cut, next, len(body)})
					}
				}
			})
		}
	}
}

func checkIncrements(t *testing.T, kind string, fold func(*testing.T) foldState, view func(foldState) string,
	body string, increments []int,
) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	fullPath := filepath.Join(dir, "full.jsonl")
	scans := newTranscriptScans(filepath.Join(dir, "cache"), &strings.Builder{})
	scans.deadline = time.Time{}
	for _, end := range increments {
		writeInPlace(t, path, body[:end])
		writeInPlace(t, fullPath, body[:end])
		state := fold(t)
		done, err := scans.fold(path, kind, state)
		if err != nil || !done {
			t.Fatalf("increments %v: fold to %d: done %v, %v", increments, end, done, err)
		}
		if got, want := view(state), fullView(t, kind, fold, view, fullPath); got != want {
			t.Fatalf("increments %v: fold to byte %d\n got  %s\n want %s", increments, end, got, want)
		}
	}
}

// foldCounting folds path from scans' checkpoint and reports the view, done,
// and how many times path was read from the top.
func foldCounting(t *testing.T, scans transcriptScans, path string) (string, bool, int) {
	t.Helper()
	whole := 0
	restore := transcript.ObserveWholeReads(func(read string) {
		if read == path {
			whole++
		}
	})
	defer restore()
	state := foldKinds[0].fold(t)
	done, err := scans.fold(path, foldKinds[0].kind, state)
	if err != nil {
		t.Fatalf("fold %s: %v", path, err)
	}
	return foldKinds[0].view(state), done, whole
}

// TestFoldKeepsProgressPastItsBudget: a render whose budget is already spent
// still shows what its checkpoint holds, says it is not done, and loses no
// work; the next render resumes from the checkpoint, not from the top.
func TestFoldKeepsProgressPastItsBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	half := strings.Join(scanFixtureLines[:6], "\n") + "\n"
	body := strings.Join(scanFixtureLines, "\n") + "\n"
	writeInPlace(t, path, half)
	scans := newTranscriptScans(filepath.Join(dir, "cache"), &strings.Builder{})
	scans.deadline = time.Time{}
	halfView, _, _ := foldCounting(t, scans, path)
	writeInPlace(t, path, body)
	spent := scans
	spent.deadline = time.Now().Add(-time.Second)
	got, done, whole := foldCounting(t, spent, path)
	if done || got != halfView || whole != 0 {
		t.Fatalf("spent budget: done %v, whole reads %d, view %s; want not done, 0, the checkpoint's %s",
			done, whole, got, halfView)
	}
	got, done, whole = foldCounting(t, scans, path)
	full := fullView(t, foldKinds[0].kind, foldKinds[0].fold, foldKinds[0].view, path)
	if !done || whole != 0 || got != full {
		t.Fatalf("next render: done %v, whole reads %d, view %s; want done, 0, the full read's %s",
			done, whole, got, full)
	}
}

// TestFoldReadsFromTheTopWhenTheFileIsNotTheOneItRead: a shrunk file, one
// rewritten to the same size, one replaced by another file with the same
// head, and a checkpoint that is corrupt or of another version are each read
// whole, and the fold still equals a full read.
func TestFoldReadsFromTheTopWhenTheFileIsNotTheOneItRead(t *testing.T) {
	body := strings.Join(scanFixtureLines, "\n") + "\n"
	other := strings.ReplaceAll(body, "claude-nonesuch-9", "claude-nonesuch-8") // inside the anchor
	cases := []struct {
		name   string
		change func(t *testing.T, path, checkpoint string)
		warns  bool
	}{
		{name: "shrunk", change: func(t *testing.T, path, _ string) {
			if err := os.WriteFile(path, []byte(scanFixtureLines[0]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "rewritten to the same size", change: func(t *testing.T, path, _ string) {
			writeInPlace(t, path, other)
		}},
		{name: "replaced by a new file", change: func(t *testing.T, path, _ string) {
			replacement := path + ".new"
			if err := os.WriteFile(replacement, []byte(body+scanFixtureLines[1]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt checkpoint", warns: true, change: func(t *testing.T, _, checkpoint string) {
			if err := os.WriteFile(checkpoint, []byte(`{"version":1,"kind":`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "checkpoint state that does not fit", warns: true, change: func(t *testing.T, _, checkpoint string) {
			rewriteCheckpoint(t, checkpoint, func(kept map[string]any) { kept["state"] = map[string]any{"meta": 7} })
		}},
		{name: "older checkpoint version", change: func(t *testing.T, _, checkpoint string) {
			rewriteCheckpoint(t, checkpoint, func(kept map[string]any) { kept["version"] = scanCheckpointVersion - 1 })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			writeInPlace(t, path, body)
			var warn strings.Builder
			scans := newTranscriptScans(filepath.Join(dir, "cache"), &warn)
			scans.deadline = time.Time{}
			foldCounting(t, scans, path)
			checkpoints := checkpointFiles(t, filepath.Join(dir, "cache"))
			if len(checkpoints) != 1 {
				t.Fatalf("checkpoints %v; want one", checkpoints)
			}
			tc.change(t, path, checkpoints[0])
			got, done, whole := foldCounting(t, scans, path)
			full := fullView(t, foldKinds[0].kind, foldKinds[0].fold, foldKinds[0].view, path)
			if !done || whole != 1 || got != full {
				t.Fatalf("done %v, whole reads %d, view %s; want done, 1, the full read's %s", done, whole, got, full)
			}
			if warned := warn.Len() != 0; warned != tc.warns {
				t.Fatalf("warnings %q; want some: %v", warn.String(), tc.warns)
			}
		})
	}
}

func rewriteCheckpoint(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept map[string]any
	if err := json.Unmarshal(body, &kept); err != nil {
		t.Fatal(err)
	}
	edit(kept)
	if body, err = json.Marshal(kept); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFoldCheckpointWriteFailureCostsOnlyARereading: a checkpoint that cannot
// be written is told on warn; the fold is still right, and the next one reads
// the file whole rather than trusting anything.
func TestFoldCheckpointWriteFailureCostsOnlyARereading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeInPlace(t, path, strings.Join(scanFixtureLines, "\n")+"\n")
	cache := filepath.Join(dir, "cache")
	if err := os.WriteFile(cache, []byte("a file where the directory goes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	scans := newTranscriptScans(cache, &warn)
	scans.deadline = time.Time{}
	full := fullView(t, foldKinds[0].kind, foldKinds[0].fold, foldKinds[0].view, path)
	for render := 1; render <= 2; render++ {
		got, done, whole := foldCounting(t, scans, path)
		if !done || whole != 1 || got != full {
			t.Fatalf("render %d: done %v, whole reads %d, view %s; want done, 1, %s", render, done, whole, got, full)
		}
	}
	if !strings.Contains(warn.String(), "scan checkpoint") {
		t.Fatalf("warnings %q; want the failed checkpoint named", warn.String())
	}
}

// TestRenderMarksASpendFloorWhileTheFoldCatchesUp: a main line rendered after
// the budget ran out keeps the harness's gauge, never a stale transcript
// reading, and marks its transcript dollars a floor.
func TestRenderMarksASpendFloorWhileTheFoldCatchesUp(t *testing.T) {
	data := input{SessionID: spendSession}
	data.TranscriptPath = spendTranscript(t, spendChildResponse)
	runtime := Runtime{
		Home: t.TempDir(), Engine: pfmengine.Claude, Prices: testPrices(t), CacheDir: t.TempDir(),
		Env: map[string]string{paths.EnvCostLedgerDir: t.TempDir()},
	}.normalized()
	runtime.scans.deadline = time.Now().Add(-time.Second)
	gauge, line, _ := renderContextLine(runtime, data, "", time.Unix(1_790_000_000, 0))
	if gauge.transcript || gauge.transcriptErr {
		t.Fatalf("gauge %+v; want the harness's gauge, the transcript neither read nor failed", gauge)
	}
	if got := stripANSICodes(line); !strings.Contains(got, "💰$?/0/0/0+?") {
		t.Fatalf("line %q; want the spend shown as a floor not yet priced, +?", got)
	}
}

// checkpointFiles is every checkpoint kept under cache.
func checkpointFiles(t *testing.T, cache string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(cache, scanCheckpointDir+"-"+strconv.Itoa(os.Getuid()), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestFoldForgetsItsCheckpointWhenTheTranscriptWillNotOpen: a transcript gone
// since its checkpoint was kept bills nothing, as a whole read of it would,
// never the sum the checkpoint held.
func TestFoldForgetsItsCheckpointWhenTheTranscriptWillNotOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.jsonl")
	writeInPlace(t, path, strings.Join(scanFixtureLines, "\n")+"\n")
	scans := newTranscriptScans(filepath.Join(dir, "cache"), &strings.Builder{})
	scans.deadline = time.Time{}
	if spend, err := readTranscriptSpend(path, testPrices(t), scans); err != nil || spend.priced == 0 {
		t.Fatalf("first read: %s, %v; want a priced spend", spendView(spend), err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	spend, err := readTranscriptSpend(path, testPrices(t), scans)
	if err == nil || spend.usd != 0 || spend.priced != 0 || spend.in != 0 || spend.tools != 0 {
		t.Fatalf("gone transcript: %s, %v; want an error and nothing billed", spendView(spend), err)
	}
}

// TestFoldReadsACheckpointWithoutItsSpendFromTheTop: a checkpoint whose fold
// lost its spend is no checkpoint: the fold reads the file whole, never
// resumes with no spend, and never panics.
func TestFoldReadsACheckpointWithoutItsSpendFromTheTop(t *testing.T) {
	body := strings.Join(scanFixtureLines, "\n") + "\n"
	for _, kind := range foldKinds {
		if kind.kind == "spend" {
			continue
		}
		t.Run(kind.kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			writeInPlace(t, path, body)
			cache := filepath.Join(dir, "cache")
			var warn strings.Builder
			scans := newTranscriptScans(cache, &warn)
			scans.deadline = time.Time{}
			if _, err := scans.fold(path, kind.kind, kind.fold(t)); err != nil {
				t.Fatal(err)
			}
			checkpoints := checkpointFiles(t, cache)
			if len(checkpoints) != 1 {
				t.Fatalf("checkpoints %v; want one", checkpoints)
			}
			rewriteCheckpoint(t, checkpoints[0], func(kept map[string]any) {
				kept["state"].(map[string]any)["spend"] = nil
			})
			state := kind.fold(t)
			done, err := scans.fold(path, kind.kind, state)
			full := fullView(t, kind.kind, kind.fold, kind.view, path)
			if err != nil || !done || kind.view(state) != full {
				t.Fatalf("done %v, %v, view %s; want the full read's %s", done, err, kind.view(state), full)
			}
			if warn.Len() == 0 {
				t.Fatal("no warning; want the checkpoint without its spend named")
			}
		})
	}
}

// TestAgentRowBehindItsBudgetClaimsNoTurnOrContext: a sub-agent fold the
// budget stopped short of its end vouches only for floors (counts, spend
// +?): its turn is unread, and its last entry, context and cache hit, which an
// older prefix would give, are unknown.
func TestAgentRowBehindItsBudgetClaimsNoTurnOrContext(t *testing.T) {
	session := filepath.Join(t.TempDir(), "session.jsonl")
	agents := transcript.SubagentsDir(session)
	if err := os.MkdirAll(agents, 0o700); err != nil {
		t.Fatal(err)
	}
	path := transcript.SubagentTranscript(agents, "a1")
	writeInPlace(t, path, strings.Join(scanFixtureLines[:11], "\n")+"\n")
	scans := newTranscriptScans(filepath.Join(t.TempDir(), "cache"), &strings.Builder{})
	scans.deadline = time.Time{}
	if prefix := readAgentActivity(session, "a1", testPrices(t), scans); prefix.context == 0 || prefix.last.IsZero() {
		t.Fatalf("prefix read: context %d, last %s; want both known", prefix.context, prefix.last)
	}
	writeInPlace(t, path, strings.Join(scanFixtureLines, "\n")+"\n")
	scans.deadline = time.Now().Add(-time.Second)
	activity := readAgentActivity(session, "a1", testPrices(t), scans)
	if activity.err != nil || activity.turn.err == nil || !activity.last.IsZero() || !activity.turn.last.IsZero() ||
		activity.context != 0 || activity.cacheHit != -1 || !activity.spend.behind || !activity.spend.partial {
		t.Fatalf("behind: err %v, turn %+v, last %s, context %d, hit %d, spend %s behind %v; want the turn "+
			"unread, no last entry, context or hit, the spend a floor", activity.err, activity.turn, activity.last,
			activity.context, activity.cacheHit, spendView(activity.spend), activity.spend.behind)
	}
}

// TestScanCheckpointsTrustOnlyThisUsersPrivateDirectory: a checkpoint
// directory that is a symlink, or (as root) one another uid owns, is never
// read: a forged checkpoint there changes nothing, the fold reads the file
// whole, and the refusal is named.
func TestScanCheckpointsTrustOnlyThisUsersPrivateDirectory(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, kept, cache string) string
	}{
		{name: "symlinked", plant: func(t *testing.T, kept, cache string) string {
			if err := os.MkdirAll(cache, 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(cache, filepath.Base(kept))
			if err := os.Symlink(kept, link); err != nil {
				t.Fatal(err)
			}
			return cache
		}},
		{name: "owned by another uid", plant: func(t *testing.T, kept, _ string) string {
			if os.Geteuid() != 0 {
				t.Skip("chown to another uid needs root; the fence runs tests as root")
			}
			if err := os.Chown(kept, 4242, 4242); err != nil {
				t.Fatal(err)
			}
			return filepath.Dir(kept)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			writeInPlace(t, path, strings.Join(scanFixtureLines, "\n")+"\n")
			forger := filepath.Join(dir, "forger")
			scans := newTranscriptScans(forger, &strings.Builder{})
			scans.deadline = time.Time{}
			foldCounting(t, scans, path)
			checkpoints := checkpointFiles(t, forger)
			if len(checkpoints) != 1 {
				t.Fatalf("checkpoints %v; want one", checkpoints)
			}
			rewriteCheckpoint(t, checkpoints[0], func(kept map[string]any) {
				kept["state"].(map[string]any)["cacheHit"] = 77
			})
			var warn strings.Builder
			victim := newTranscriptScans(tc.plant(t, filepath.Dir(checkpoints[0]), filepath.Join(dir, "cache")), &warn)
			victim.deadline = time.Time{}
			got, done, whole := foldCounting(t, victim, path)
			full := fullView(t, foldKinds[0].kind, foldKinds[0].fold, foldKinds[0].view, path)
			if !done || whole != 1 || got != full {
				t.Fatalf("done %v, whole reads %d, view %s; want done, 1, the full read's %s", done, whole, got, full)
			}
			if !strings.Contains(warn.String(), "scan checkpoint") {
				t.Fatalf("warnings %q; want the refused directory named", warn.String())
			}
		})
	}
}

// TestScanCheckpointsOutlivingTheirAgeArePruned: a render's scans remove the
// checkpoints (and a killed render's scratch files) not written for a day, and
// keep the rest.
func TestScanCheckpointsOutlivingTheirAgeArePruned(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, scanCheckpointDir+"-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, scratch, fresh := filepath.Join(dir, "spend-old.json"), filepath.Join(dir, ".spend-x.json.tmp-1"),
		filepath.Join(dir, "spend-new.json")
	for _, file := range []string{stale, scratch, fresh} {
		if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	for _, file := range []string{stale, scratch} {
		if err := os.Chtimes(file, old, old); err != nil {
			t.Fatal(err)
		}
	}
	var warn strings.Builder
	newTranscriptScans(cache, &warn)
	for file, want := range map[string]bool{stale: false, scratch: false, fresh: true} {
		if _, err := os.Stat(file); (err == nil) != want {
			t.Fatalf("%s present %v; want %v (warnings %q)", file, err == nil, want, warn.String())
		}
	}
}

// TestScanCheckpointPruneCapsTheTotalAndWaitsItsTurn: past the byte cap the
// oldest checkpoints go first until the rest fit; a prune within the hour of
// the last does nothing.
func TestScanCheckpointPruneCapsTheTotalAndWaitsItsTurn(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for index, name := range []string{"a.json", "b.json", "c.json"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
			t.Fatal(err)
		}
		written := now.Add(time.Duration(index-3) * time.Minute) // a oldest, c newest
		if err := os.Chtimes(file, written, written); err != nil {
			t.Fatal(err)
		}
	}
	var warn strings.Builder
	pruneStaleFiles(dir, now, time.Hour, 150, &warn)
	for name, want := range map[string]bool{"a.json": false, "b.json": false, "c.json": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Fatalf("%s present %v; want %v (warnings %q)", name, err == nil, want, warn.String())
		}
	}
	pruneStaleFiles(dir, now.Add(30*time.Minute), time.Hour, 0, &warn)
	if _, err := os.Stat(filepath.Join(dir, "c.json")); err != nil || warn.Len() != 0 {
		t.Fatalf("second prune within the hour: c.json %v, warnings %q; want it kept, none", err, warn.String())
	}
}
