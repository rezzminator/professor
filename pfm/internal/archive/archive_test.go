package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// fakeKills is the killed set with an audit trail of what was retired.
type fakeKills struct {
	rows     []KilledChat
	unkilled []string
	failOn   string
}

func (kills *fakeKills) Killed(context.Context) ([]KilledChat, error) {
	return kills.rows, nil
}

func (kills *fakeKills) Unkill(_ context.Context, id string) error {
	if id == kills.failOn {
		return errors.New("store is busy")
	}
	kills.unkilled = append(kills.unkilled, id)
	return nil
}

// emptyProc reports no processes: the fixtures declare liveness through the
// sid crumbs instead, which is the reading that does not need a fake /proc.
//
// failPIDs lets a test drive the "process table unreadable" reading a real
// jailed or denied /proc can produce — the case LiveSessions must report as
// an error rather than silently reading as "no live processes".
type emptyProc struct {
	failPIDs bool
}

func (proc emptyProc) PIDs() ([]int, error) {
	if proc.failPIDs {
		return nil, errors.New("process table unreadable")
	}
	return nil, nil
}
func (emptyProc) Cmdline(int) ([]string, error)          { return nil, nil }
func (emptyProc) Environ(int) (map[string]string, error) { return nil, nil }
func (emptyProc) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (emptyProc) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }

func archiveJail(t *testing.T) paths.Values {
	t.Helper()
	root := t.TempDir()
	values := paths.Values{
		Home: filepath.Join(root, "home"),
		Roots: map[pfmengine.ID][]string{
			pfmengine.Claude:   {filepath.Join(root, "home", ".cc", "1", "projects")},
			pfmengine.Codex:    {filepath.Join(root, "home", ".codex")},
			pfmengine.OpenCode: {filepath.Join(root, "home", ".local", "share", "opencode")},
		},
		SIDDir:     filepath.Join(root, "sid"),
		ArchiveDir: filepath.Join(root, "home", ".claude-archive"),
		TmuxDir:    filepath.Join(root, "tmux"),
		ProcRoot:   filepath.Join(root, "proc"),
	}
	for _, directory := range []string{
		filepath.Join(values.Roots[pfmengine.Claude][0], "-home-user-work-x"),
		filepath.Join(values.Roots[pfmengine.Codex][0], "sessions", "2026", "08", "15"),
		values.SIDDir,
		filepath.Join(values.Home, ".claude"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return values
}

// OpenCode stores every session in one shared SQLite database, so the file
// archive cannot move one session without moving all of them. A killed
// OpenCode session must therefore stay killed and be reported as unsupported;
// treating it as an orphan and retiring the kill makes the chat come back.
func TestArchivePreservesUnsupportedOpenCodeKill(t *testing.T) {
	values := archiveJail(t)
	kills := &fakeKills{rows: []KilledChat{{ID: "ses-opencode", Engine: pfmengine.OpenCode}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}

	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans) != 0 || report.Unkilled != 0 || len(kills.unkilled) != 0 {
		t.Fatalf("OpenCode kill was treated as disposable: report=%+v unkilled=%v", report, kills.unkilled)
	}
	if len(report.Unsupported) != 1 || report.Unsupported[0] != "ses-opencode" {
		t.Fatalf("OpenCode archive limitation was hidden: report=%+v", report)
	}
}

// TestRunRecordsTheArchivesStateTransitions: Run walks the state door (spec
// § Middleware, `state`) — requested to planned to done — one comp=state
// record per phase with dur_ms; an empty killed set plans and moves nothing.
func TestRunRecordsTheArchivesStateTransitions(t *testing.T) {
	ctx, recorder := obs.Test(t)
	values := archiveJail(t)
	runner, err := New(Dependencies{Paths: values, Kills: &fakeKills{}, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, Options{Apply: false}); err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "archive" {
			continue
		}
		next, _ := record.Field("next")
		path = append(path, next.(string))
	}
	if got := strings.Join(path, ","); got != "planned,done" {
		t.Fatalf("archive state path = %s, want planned,done: %s", got, recorder.Raw())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredArchiveRootsExcludeLegacyPrimaryAlias(t *testing.T) {
	values := archiveJail(t)
	runner, err := New(Dependencies{
		Paths:            values,
		Kills:            &fakeKills{},
		Proc:             emptyProc{},
		ExactClaudeRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	roots := runner.claudeRoots()
	if len(roots) != 1 || roots[0] != values.Roots[pfmengine.Claude][0] {
		t.Fatalf("configured archive roots = %#v, want exact roster %#v", roots, values.Roots[pfmengine.Claude])
	}
}

// The whole killed-chat contract in one run: the dry run moves nothing, the
// apply moves exactly the resolvable dead chats, a LIVE killed chat is left on
// disk, and every decided id leaves the killed list — including the live one,
// which belongs back in the picker.
func TestArchiveMovesOnlyResolvableDeadKilledChats(t *testing.T) {
	values := archiveJail(t)
	const (
		deadID  = "11111111-1111-4111-8111-111111111111"
		liveID  = "22222222-2222-4222-8222-222222222222"
		ghostID = "33333333-3333-4333-8333-333333333333"
		codexID = "44444444-4444-4444-8444-444444444444"
		rollout = "rollout-2026-08-15T10-00-00-44444444-4444-4444-8444-444444444444.jsonl"
		project = "-home-user-work-x"
	)
	transcripts := filepath.Join(values.Roots[pfmengine.Claude][0], project)
	writeFile(t, filepath.Join(transcripts, deadID+".jsonl"), "{}\n")
	writeFile(t, filepath.Join(transcripts, liveID+".jsonl"), "{}\n")
	writeFile(
		t,
		filepath.Join(values.Roots[pfmengine.Codex][0], "sessions", "2026", "08", "15", rollout),
		"{}\n",
	)
	// The live chat states itself through its socket crumb, exactly as a
	// running chat does.
	writeFile(
		t,
		filepath.Join(values.SIDDir, "cc-1800000001-42-1"),
		filepath.Join(transcripts, liveID+".jsonl")+"\n",
	)
	writeFile(
		t,
		filepath.Join(values.Home, ".claude", "history.jsonl"),
		`{"display":"one","sid":"`+deadID+`"}`+"\n"+
			`{"display":"two","sid":"`+liveID+`"}`+"\n",
	)
	writeFile(
		t,
		filepath.Join(values.Roots[pfmengine.Codex][0], "session_index.jsonl"),
		`{"id":"`+codexID+`","thread_name":"gone"}`+"\n"+
			`{"id":"55555555-5555-4555-8555-555555555555","thread_name":"kept"}`+"\n",
	)

	kills := &fakeKills{rows: []KilledChat{
		{ID: deadID, Engine: "cc"},
		{ID: liveID, Engine: "cc"},
		{ID: ghostID, Engine: "cc"},
		{ID: codexID, Engine: "cx"},
	}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := runner.Run(context.Background(), Options{})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(preview.Moves) != 2 {
		t.Fatalf("dry run planned %d moves, want 2: %#v", len(preview.Moves), preview.Moves)
	}
	if _, err := os.Stat(filepath.Join(transcripts, deadID+".jsonl")); err != nil {
		t.Fatalf("the dry run moved a file: %v", err)
	}
	if len(kills.unkilled) != 0 {
		t.Fatalf("the dry run retired kills: %v", kills.unkilled)
	}

	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(transcripts, deadID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("the dead chat was not archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(transcripts, liveID+".jsonl")); err != nil {
		t.Fatalf("a LIVE chat's transcript was archived out from under it: %v", err)
	}
	if len(report.Live) != 1 || report.Live[0] != liveID {
		t.Fatalf("live set = %v, want [%s]", report.Live, liveID)
	}
	if len(report.Orphans) != 1 || report.Orphans[0] != ghostID {
		t.Fatalf("orphans = %v, want [%s]", report.Orphans, ghostID)
	}
	if report.Unkilled != 4 {
		t.Fatalf("kills retired = %d, want 4 (every decided id)", report.Unkilled)
	}

	archived := filepath.Join(values.ArchiveDir, "claude", project, deadID+".jsonl")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("archived file is not where the manifest says: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(values.ArchiveDir, "codex", rollout),
	); err != nil {
		t.Fatalf("the codex rollout was not archived: %v", err)
	}

	history, err := os.ReadFile(filepath.Join(values.Home, ".claude", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(history), deadID) {
		t.Fatal("history.jsonl still carries the archived chat's prompts")
	}
	if !strings.Contains(string(history), liveID) {
		t.Fatal("history.jsonl lost a live chat's prompts")
	}
	index, err := os.ReadFile(filepath.Join(values.Roots[pfmengine.Codex][0], "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), codexID) {
		t.Fatal("the codex index still points at a moved rollout")
	}
	if !strings.Contains(string(index), "kept") {
		t.Fatal("the codex index lost an untouched row")
	}
	if len(report.SidecarBackups) != 2 {
		t.Fatalf("sidecar backups = %v, want two files copied", report.SidecarBackups)
	}
}

// A second run finds nothing left to do — an archive you cannot re-run is a
// tool you have to remember the state of.
func TestArchiveIsIdempotent(t *testing.T) {
	values := archiveJail(t)
	const id = "11111111-1111-4111-8111-111111111111"
	transcripts := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
	writeFile(t, filepath.Join(transcripts, id+".jsonl"), "{}\n")
	kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: "cc"}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	kills.rows = nil
	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(report.Moves) != 0 {
		t.Fatalf("second run planned %d moves", len(report.Moves))
	}
}

// Restore is the whole reason this is an archive and not a delete.
func TestRestorePutsAChatBackWhereItWas(t *testing.T) {
	values := archiveJail(t)
	const id = "11111111-1111-4111-8111-111111111111"
	transcripts := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
	original := filepath.Join(transcripts, id+".jsonl")
	writeFile(t, original, "{\"a\":1}\n")
	kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: "cc"}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	row, err := Restore(values.ArchiveDir, id)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if row.Original != original {
		t.Fatalf("restored to %s, want %s", row.Original, original)
	}
	content, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("the restored file is not readable: %v", err)
	}
	if string(content) != "{\"a\":1}\n" {
		t.Fatalf("restored content = %q", content)
	}
	if _, err := Restore(values.ArchiveDir, "not-in-the-manifest"); err == nil {
		t.Fatal("Restore() accepted an id the manifest never recorded")
	}
}

// Subagent mode is age-gated, because a running subagent appends to its
// transcript for as long as it runs.
func TestArchiveSubagentsRespectsTheAgeGate(t *testing.T) {
	values := archiveJail(t)
	project := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
	old := filepath.Join(project, "aaaaaaaa-0000-4000-8000-000000000001.jsonl")
	fresh := filepath.Join(project, "bbbbbbbb-0000-4000-8000-000000000002.jsonl")
	chat := filepath.Join(project, "cccccccc-0000-4000-8000-000000000003.jsonl")
	writeFile(t, old, `{"isSidechain":true,"type":"user"}`+"\n")
	writeFile(t, fresh, `{"isSidechain":true,"type":"user"}`+"\n")
	writeFile(t, chat, `{"type":"user","message":"hello"}`+"\n")
	past := time.Now().Add(-72 * time.Hour)
	for _, path := range []string{old, chat} {
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}

	kills := &fakeKills{}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), Options{
		Apply:     true,
		Subagents: true,
		OlderThan: 48 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Moves) != 1 || report.Moves[0].Source != old {
		t.Fatalf("moves = %#v, want only the aged sidechain", report.Moves)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a fresh sidechain was archived: %v", err)
	}
	if _, err := os.Stat(chat); err != nil {
		t.Fatalf("a real chat transcript was archived as a sidechain: %v", err)
	}
	if len(kills.unkilled) != 0 {
		t.Fatalf("subagent mode touched the killed list: %v", kills.unkilled)
	}
}

// A live subagent transcript is left alone even when its file is old enough:
// liveness is re-read at run time and outranks the age gate.
func TestArchiveSubagentsSkipsLiveTranscripts(t *testing.T) {
	values := archiveJail(t)
	project := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	path := filepath.Join(project, id+".jsonl")
	writeFile(t, path, `{"isSidechain":true}`+"\n")
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(values.SIDDir, "cc-1800000001-42-1"), path+"\n")

	runner, err := New(Dependencies{
		Paths: values,
		Kills: &fakeKills{},
		Proc:  emptyProc{},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), Options{
		Apply:     true,
		Subagents: true,
		OlderThan: 48 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Moves) != 0 {
		t.Fatalf("a live transcript was archived: %#v", report.Moves)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the live transcript is gone: %v", err)
	}
}

// L1-F1: when the live-session reading itself could not run — an unreadable
// process table — Run must refuse to decide anything at all rather than treat
// the failure as "no chats are live". Nothing on disk moves and no kill is
// retired.
func TestArchiveRefusesToDecideWhenTheLiveReadingFails(t *testing.T) {
	values := archiveJail(t)
	const id = "11111111-1111-4111-8111-111111111111"
	transcripts := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
	original := filepath.Join(transcripts, id+".jsonl")
	writeFile(t, original, "{}\n")
	kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: "cc"}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{failPIDs: true}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), Options{Apply: true}); err == nil {
		t.Fatal("Run() with an unreadable process table returned no error")
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("Run() moved a transcript despite a failed live reading: %v", err)
	}
	if len(kills.unkilled) != 0 {
		t.Fatalf("Run() retired a kill despite a failed live reading: %v", kills.unkilled)
	}
}

// L1-F2: a transcript lookup that could not run — here, the claude projects
// root is unreadable — must land the killed id in Unresolved, reported and
// never un-killed, instead of being classed an orphan and having its kill row
// retired.
func TestArchiveMarksUnresolvedWhenTheTranscriptLookupCannotRun(t *testing.T) {
	values := archiveJail(t)
	const id = "11111111-1111-4111-8111-111111111111"
	root := values.Roots[pfmengine.Claude][0]
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: "cc"}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}

	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("a lookup that could not run was classed an orphan: %#v", report)
	}
	if len(report.Unresolved) != 1 || report.Unresolved[0] != id {
		t.Fatalf("Unresolved = %v, want [%s]", report.Unresolved, id)
	}
	if len(kills.unkilled) != 0 {
		t.Fatalf("an unresolved lookup's kill was retired: %v", kills.unkilled)
	}
}
