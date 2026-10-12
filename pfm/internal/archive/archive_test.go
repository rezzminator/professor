package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

func TestArchiveUsesRuntimeClaudeRoots(t *testing.T) {
	values := archiveJail(t)
	runner, err := New(Dependencies{
		Paths: values,
		Kills: &fakeKills{},
		Proc:  emptyProc{},
	})
	if err != nil {
		t.Fatal(err)
	}
	roots := runner.claudeRoots()
	if len(roots) != 1 || roots[0] != values.Roots[pfmengine.Claude][0] {
		t.Fatalf("archive roots = %#v, want runtime roots %#v", roots, values.Roots[pfmengine.Claude])
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
	// A failed later intent must not hide the older recoverable archive.
	if err := AppendManifest(values.ArchiveDir, Move{
		ID: id, Engine: "cc", Source: filepath.Join(transcripts, "missing.jsonl"),
		Target: filepath.Join(values.ArchiveDir, "missing-target.jsonl"),
	}, "later"); err != nil {
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

func TestArchiveReplaysSidecarCleanupBeforeRetiringKill(t *testing.T) {
	for _, name := range []string{"history.jsonl", "session_index.jsonl"} {
		t.Run(name, func(t *testing.T) {
			values := archiveJail(t)
			const id = "88888888-8888-4888-8888-888888888888"
			source := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
			writeFile(t, source, "{}\n")
			sidecar := filepath.Join(values.Home, ".claude", name)
			field := "sessionId"
			if name == "session_index.jsonl" {
				sidecar = filepath.Join(values.Roots[pfmengine.Codex][0], name)
				field = "id"
			}
			owned := `{"` + field + `":"` + id + `","display":"archived"}` + "\n"
			oversized := `{"` + field + `":"99999999-9999-4999-8999-999999999999","display":"` +
				strings.Repeat("x", 8*1024*1024) + `"}` + "\n"
			writeFile(t, sidecar, owned+oversized)
			kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: pfmengine.Claude}}}
			runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			first, err := runner.Run(context.Background(), Options{Apply: true})
			if err == nil || !strings.Contains(err.Error(), "token too long") ||
				len(first.Moves) != 1 || !first.Moves[0].Applied || len(kills.unkilled) != 0 {
				t.Fatalf("fixture did not fail cleanup after relocation: report=%+v error=%v", first, err)
			}
			if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fixture source was not relocated: %v", err)
			}
			retry, err := runner.Run(context.Background(), Options{Apply: true})
			if err == nil || len(kills.unkilled) != 0 || len(retry.Orphans) != 0 {
				t.Fatalf("retry retired kill with pending sidecar cleanup: report=%+v error=%v retired=%v",
					retry, err, kills.unkilled)
			}
			// The chat resumes while its cleanup is pending: it keeps its kill and is
			// reported, and the run still decides the rest of the killed set.
			const otherID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			other := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", otherID+".jsonl")
			writeFile(t, other, "{}\n")
			kills.rows = append(kills.rows, KilledChat{ID: otherID, Engine: pfmengine.Claude})
			kept := `{"` + field + `":"99999999-9999-4999-8999-999999999999","display":"kept"}` + "\n"
			otherLine := `{"` + field + `":"` + otherID + `","display":"other"}` + "\n"
			writeFile(t, sidecar, owned+kept+otherLine)
			crumb := filepath.Join(values.SIDDir, "cc-1800000001-42-1")
			writeFile(t, crumb, source+"\n")
			paused, pausedErr := runner.Run(context.Background(), Options{Apply: true})
			if pausedErr != nil || len(paused.Live) != 1 ||
				!strings.Contains(paused.UnresolvedErrors[id], "pending") ||
				strings.Join(kills.unkilled, ",") != otherID {
				t.Errorf(
					"a live chat with pending cleanup blocked the run or lost its kill: report=%+v error=%v retired=%v",
					paused,
					pausedErr,
					kills.unkilled,
				)
			}
			kills.rows = kills.rows[:1]
			if err := os.Remove(crumb); err != nil {
				t.Fatal(err)
			}
			// The chat has stopped; replay must clean its metadata without moving again.
			finished, err := runner.Run(context.Background(), Options{Apply: true})
			if err != nil || finished.Unkilled != 1 || strings.Join(kills.unkilled, ",") != otherID+","+id {
				t.Fatalf("cleanup replay did not finish retirement: report=%+v error=%v retired=%v",
					finished, err, kills.unkilled)
			}
			content, err := os.ReadFile(sidecar)
			if err != nil || strings.TrimSpace(string(content)) != strings.TrimSpace(kept) {
				t.Fatalf("cleanup replay lost or retained metadata: content=%q error=%v", content, err)
			}
			rows, err := ReadManifest(values.ArchiveDir)
			if err != nil || len(rows) != 2 || rows[0].ID != id {
				t.Fatalf("replay rewrote relocation history: rows=%+v error=%v", rows, err)
			}
			if _, err := Restore(values.ArchiveDir, id); err != nil {
				t.Fatalf("cleanup retry lost transcript recovery: %v", err)
			}
		})
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
// root cannot be read as a directory — must land the killed id in Unresolved, reported and
// never un-killed, instead of being classed an orphan and having its kill row
// retired.
func TestArchiveMarksUnresolvedWhenTheTranscriptLookupCannotRun(t *testing.T) {
	values := archiveJail(t)
	const id = "11111111-1111-4111-8111-111111111111"
	root := values.Roots[pfmengine.Claude][0]
	// A regular file where the projects root should be: reading it as a
	// directory fails with ENOTDIR for every user. chmod 000 would prove
	// nothing under root, which is what the fence runs as.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

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

// TestArchiveFindsAClaudeTranscriptBehindASymlinkedProject: the lookup is
// transcript.FindSessions, so a project directory that is a symlink is read
// like any other (INTENDED: it used to be skipped as no directory).
func TestArchiveFindsAClaudeTranscriptBehindASymlinkedProject(t *testing.T) {
	values := archiveJail(t)
	const id = "22222222-2222-4222-8222-222222222222"
	elsewhere := filepath.Join(t.TempDir(), "-work-linked")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(values.Roots[pfmengine.Claude][0], "-work-linked")
	if err := os.Symlink(elsewhere, project); err != nil {
		t.Fatal(err)
	}
	runner, err := New(Dependencies{Paths: values, Kills: &fakeKills{}, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	if path, err := runner.findClaudeTranscript(id); err != nil || path != filepath.Join(project, id+".jsonl") {
		t.Fatalf("findClaudeTranscript = %q, %v; want the transcript behind the linked project", path, err)
	}
}

func TestArchiveFailedMovesRemainKilledAndRecoverable(t *testing.T) {
	for _, failure := range []string{"occupied target", "manifest unavailable", "directory sync unavailable"} {
		t.Run(failure, func(t *testing.T) {
			values := archiveJail(t)
			const id = "11111111-1111-4111-8111-111111111111"
			source := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
			writeFile(t, source, "transcript\n")
			kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: pfmengine.Claude}}}
			runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			if failure == "occupied target" {
				writeFile(t, runner.targetFor(pfmengine.Claude, source), "existing archive\n")
			} else if failure == "directory sync unavailable" {
				originalSync := syncManifestParents
				t.Cleanup(func() { syncManifestParents = originalSync })
				syncManifestParents = func(string) error { return errors.New("directory sync unavailable") }
			} else if err := os.MkdirAll(ManifestPath(values.ArchiveDir), 0o700); err != nil {
				t.Fatal(err)
			}
			report, err := runner.Run(context.Background(), Options{Apply: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Moves) != 1 || report.Moves[0].Failed == "" {
				t.Fatalf("failure not reported: %+v", report)
			}
			content, err := os.ReadFile(source)
			if err != nil || string(content) != "transcript\n" {
				t.Errorf("failed archive lost original transcript: %q, %v", content, err)
			}
			if report.Unkilled != 0 || len(kills.unkilled) != 0 {
				t.Errorf("failed move retired its kill: %+v, %v", report, kills.unkilled)
			}
			if report.Moves[0].Applied {
				t.Errorf("unrecorded failed move reported Applied: %+v", report.Moves[0])
			}
		})
	}
}

func TestArchiveUnresolvedRetainsItsCause(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			values := archiveJail(t)
			const id = "11111111-1111-4111-8111-111111111111"
			if engine == pfmengine.Claude {
				root := values.Roots[engine][0]
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				writeFile(t, root, "not a directory")
			} else {
				path := filepath.Join(values.Roots[engine][0], "sessions", "loop-"+id+".jsonl")
				if err := os.Symlink(filepath.Base(path), path); err != nil {
					t.Fatal(err)
				}
			}
			kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: engine}}}
			runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runner.Run(context.Background(), Options{Apply: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Unresolved) != 1 || len(report.Orphans) != 0 || len(kills.unkilled) != 0 {
				t.Fatalf("unreadable transcript was not kept unresolved: %+v kills=%v", report, kills.unkilled)
			}
			if !strings.Contains(fmt.Sprintf("%+v", report), "directory") &&
				!strings.Contains(fmt.Sprintf("%+v", report), "symbolic links") {
				t.Fatalf("unresolved cause discarded: %+v", report)
			}
		})
	}
}

func TestArchivePreservesAnExternalSidecarAppendAtCommit(t *testing.T) {
	for _, name := range []string{
		"history.jsonl", "history alias", "history writer active", "history writer stale",
		"history lease replaced", "history slow prune", "session_index.jsonl", "index replaced", "index held append",
	} {
		t.Run(name, func(t *testing.T) {
			values := archiveJail(t)
			const archived = "11111111-1111-4111-8111-111111111111"
			const unrelated = "22222222-2222-4222-8222-222222222222"
			isIndex := name == "session_index.jsonl" || name == "index replaced" || name == "index held append"
			path := filepath.Join(values.Home, ".claude", "history.jsonl")
			if isIndex {
				path = filepath.Join(values.Roots[pfmengine.Codex][0], "session_index.jsonl")
			}
			record := func(id, label string) string {
				if isIndex {
					return fmt.Sprintf(`{"id":%q,"thread_name":%q,"updated_at":"2026-08-15T10:00:00Z"}`+"\n", id, label)
				}
				return fmt.Sprintf(`{"sessionId":%q,"display":%q,"pastedContents":{},`+
					`"timestamp":1800000000000,"project":"fixture-project"}`+"\n", id, label)
			}
			owned := record(archived, "archived")
			kept := record("33333333-3333-4333-8333-333333333333", "kept")
			appended := record(unrelated, "external append")
			writeFile(t, path, owned+kept)
			if name == "history alias" {
				target := filepath.Join(values.Home, "actual-history")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Fatal(err)
			}
			if name == "history writer active" || name == "history writer stale" {
				if err := os.Mkdir(realPath+".lock", 0o700); err != nil {
					t.Fatal(err)
				}
				if name == "history writer stale" {
					past := time.Now().Add(-time.Minute)
					if err := os.Chtimes(realPath+".lock", past, past); err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() {
					if err := os.Remove(realPath + ".lock"); err != nil {
						t.Errorf("release native writer: %v", err)
					}
				})
			}
			// Native Claude uses a realpath-keyed mkdir lease; native Codex uses O_APPEND,
			// not a shared filesystem lock. These are actual jailed filesystem operations.
			appendExternal := func() error {
				file, err := os.OpenFile(realPath, os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					return err
				}
				_, writeErr := file.WriteString(appended)
				return errors.Join(writeErr, file.Close())
			}
			var heldAppend *os.File
			if name == "index held append" {
				heldAppend, err = os.OpenFile(realPath, os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := heldAppend.Close(); err != nil {
						t.Errorf("close external append descriptor: %v", err)
					}
				})
			}
			var slow *clock.Fake
			if name == "history slow prune" {
				slow = clock.NewFake(time.Now())
				originalClock := leaseClock
				leaseClock = slow
				t.Cleanup(func() { leaseClock = originalClock })
			}
			original := beforePruneCommit
			t.Cleanup(func() { beforePruneCommit = original })
			blocked := false
			entered := false
			beforePruneCommit = func(string) error {
				entered = true
				if name == "index held append" {
					return nil // The native descriptor stays open across our commit.
				}
				if name == "history slow prune" {
					// Carry the producer's ten-second stale-takeover boundary on the
					// lease clock: eleven seconds pass, and every heartbeat tick is
					// given its chance to refresh the lease before time moves on.
					for step := 1; step <= 11; step++ {
						slow.Advance(time.Second)
						if step%2 == 0 && !awaitLeaseRefresh(realPath+".lock", slow.Now()) {
							return fmt.Errorf("the lease was not refreshed by the heartbeat due at %s", slow.Now())
						}
					}
					info, err := os.Stat(realPath + ".lock")
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					if err == nil && slow.Now().Sub(info.ModTime()) >= 10*time.Second {
						if err := os.Remove(realPath + ".lock"); err != nil {
							return err
						}
					}
				}
				if name == "index replaced" {
					return atomicfile.Write(realPath, []byte(owned+kept+appended), 0o600)
				}
				if name == "history lease replaced" {
					if err := os.Rename(realPath+".lock", realPath+".displaced-lock"); errors.Is(err, os.ErrNotExist) {
						return nil // Unfixed prune holds no lease: the later assertion still fails.
					} else if err != nil {
						return err
					}
					return os.Mkdir(realPath+".lock", 0o700)
				}
				if !isIndex {
					if err := os.Mkdir(realPath+".lock", 0o700); errors.Is(err, os.ErrExist) {
						blocked = true
						return nil
					} else if err != nil {
						return err
					}
					return errors.Join(appendExternal(), os.Remove(realPath+".lock"))
				}
				return appendExternal()
			}
			runner, err := New(Dependencies{Paths: values, Kills: &fakeKills{}, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			dropped, _, err := runner.pruneLines(path, []string{archived})
			if name == "history writer active" || name == "history writer stale" ||
				name == "history lease replaced" || name == "index replaced" {
				if err == nil || dropped != 0 {
					t.Errorf("lost sidecar protection was reported as success: dropped=%d error=%v", dropped, err)
				}
				expected := owned + kept
				if name == "index replaced" {
					expected += appended
				}
				content, readErr := os.ReadFile(path)
				if readErr != nil || string(content) != expected {
					t.Errorf("unsafe prune modified the protected sidecar: content=%q error=%v", content, readErr)
				}
				if !isIndex {
					if _, statErr := os.Stat(realPath + ".lock"); statErr != nil {
						t.Errorf("prune removed another history writer's lease: %v", statErr)
					}
				}
				return
			}
			if err != nil || dropped != 1 || !entered {
				t.Fatalf(
					"prune did not reach its commit boundary: dropped=%d entered=%v error=%v",
					dropped,
					entered,
					err,
				)
			}
			if !isIndex {
				if !blocked {
					t.Error("native history writer entered the scan-to-replacement interval")
				} else {
					if err := os.Mkdir(realPath+".lock", 0o700); err != nil {
						t.Fatalf("history lease was not released: %v", err)
					}
					if err := errors.Join(appendExternal(), os.Remove(realPath+".lock")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if heldAppend != nil {
				if _, err := heldAppend.WriteString(appended); err != nil {
					t.Fatal(err)
				}
			}
			content, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(content), archived) ||
				!strings.Contains(string(content), kept) || !strings.Contains(string(content), appended) {
				t.Fatalf("sidecar commit lost an unrelated external append: content=%q error=%v", content, err)
			}
		})
	}
}

// awaitLeaseRefresh waits, a bounded real while, for the heartbeat to stamp the
// lease at tick; a heartbeat that never does leaves the stale lease to the
// producer's takeover, which the caller's assertion then reports.
// awaitLeaseRefresh reports whether the heartbeat refreshed the lease for the
// tick due at tick; a heartbeat that never answers is false, never a pass.
func awaitLeaseRefresh(lock string, tick time.Time) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(lock); err == nil && !info.ModTime().Before(tick.Truncate(time.Microsecond)) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestMoveFileRefusesAnOccupiedPublicationTarget(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	writeFile(t, source, "second transcript")
	writeFile(t, target, "first transcript")
	if err := moveFile(source, target); err == nil {
		t.Fatal("archive publication overwrote a concurrent winner")
	}
	for path, want := range map[string]string{source: "second transcript", target: "first transcript"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("publication lost transcript %s: got=%q err=%v", path, got, err)
		}
	}
}

// The observation seam pauses the writer at its pre-write publication boundary.
func TestMoveFileCrossFilesystemPublishesOnlyCompleteTarget(t *testing.T) {
	originalLink, originalBefore := linkArchiveFile, beforeArchiveCopyWrite
	t.Cleanup(func() { linkArchiveFile, beforeArchiveCopyWrite = originalLink, originalBefore })
	linkArchiveFile = func(string, string) error { return syscall.EXDEV }
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	writeFile(t, source, "complete transcript\n")
	observed := false
	beforeArchiveCopyWrite = func() {
		observed = true
		if content, err := os.ReadFile(target); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("incomplete final archive target visible before copy write: content=%q err=%v", content, err)
		}
	}
	if err := moveFile(source, target); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("fixture never reached the cross-filesystem write boundary")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "complete transcript\n" {
		t.Fatalf("incomplete publication: content=%q err=%v", got, err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful move retained source: %v", err)
	}
}

func TestMoveFileKeepsSourceWhenPublicationDurabilityFails(t *testing.T) {
	originalLink, originalSync := linkArchiveFile, syncArchivePublication
	t.Cleanup(func() { linkArchiveFile, syncArchivePublication = originalLink, originalSync })
	for _, copyFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross-filesystem=%v", copyFile), func(t *testing.T) {
			linkArchiveFile = originalLink
			if copyFile {
				linkArchiveFile = func(string, string) error { return syscall.EXDEV }
			}
			syncArchivePublication = func(string) error { return errors.New("publication directory sync unavailable") }
			root := t.TempDir()
			source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
			writeFile(t, source, "durable original")
			err := moveFile(source, target)
			if err == nil || !strings.Contains(err.Error(), "publication directory sync unavailable") {
				t.Errorf("publication durability failure not reported: %v", err)
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != "durable original" {
				t.Errorf("source unlinked before publication durability: content=%q err=%v", got, err)
			}
		})
	}
}
