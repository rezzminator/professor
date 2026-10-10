package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A writer may begin at any instant between the live snapshot and the end of
// the relocation. Each schedule starts one at a different transaction door —
// a real append descriptor, a by-path append that recreates the file, a
// registration crumb — and the transcript must end up where its writer can
// still reach every byte, with the kill kept.
func TestArchiveExcludesAWriterThatStartsDuringRelocation(t *testing.T) {
	const id = "55555555-5555-4555-8555-555555555555"
	for _, schedule := range []string{
		"after the live snapshot",
		"after publication",
		"after removal, registered",
		"after removal, recreated by path",
		"after removal, unregistered descriptor",
		"parent session live at the snapshot",
	} {
		for _, subagent := range []bool{false, true} {
			if schedule == "parent session live at the snapshot" && !subagent {
				continue // A live killed chat is TestArchiveMovesOnlyResolvableDeadKilledChats.
			}
			name := schedule + "/killed chat"
			if subagent {
				name = schedule + "/subagent"
			}
			t.Run(name, func(t *testing.T) {
				values := archiveJail(t)
				project := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
				path := filepath.Join(project, id+".jsonl")
				initial := `{"type":"user","sessionId":"` + id + `"}` + "\n"
				if subagent {
					path = filepath.Join(project, id, "subagents", "agent-child.jsonl")
					initial = `{"type":"user","isSidechain":true,"agentId":"child","sessionId":"` + id + `"}` + "\n"
				}
				writeFile(t, path, initial)
				past := time.Now().Add(-72 * time.Hour)
				if err := os.Chtimes(path, past, past); err != nil {
					t.Fatal(err)
				}
				crumb := filepath.Join(values.SIDDir, "cc-1800000001-42-1")
				register := func() { writeFile(t, crumb, filepath.Join(project, id+".jsonl")+"\n") }
				if schedule == "parent session live at the snapshot" {
					register()
				}
				var writer *os.File
				t.Cleanup(func() {
					if writer != nil {
						if err := writer.Close(); err != nil {
							t.Errorf("close the writer: %v", err)
						}
					}
				})
				open := func() {
					var err error
					if writer, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600); err != nil {
						t.Fatalf("writer could not open the transcript: %v", err)
					}
				}
				appendHeld := func() {
					if _, err := writer.WriteString(
						`{"type":"assistant","message":"resumed writer"}` + "\n",
					); err != nil {
						t.Fatalf("held writer append: %v", err)
					}
				}
				originalLink, originalRemove := linkArchiveFile, removeArchiveSource
				t.Cleanup(func() { linkArchiveFile, removeArchiveSource = originalLink, originalRemove })
				started := false
				linkArchiveFile = func(source, target string) error {
					err := originalLink(source, target)
					if source != path || started {
						return err
					}
					switch schedule {
					case "after publication":
						started = true
						open()
						appendHeld()
						register()
					case "after removal, registered", "after removal, unregistered descriptor":
						open() // The writer has the file; it appends only after the unlink.
					}
					return err
				}
				removeArchiveSource = func(source string) error {
					err := originalRemove(source)
					if source != path || started || err != nil {
						return err
					}
					started = true
					switch schedule {
					case "after removal, registered":
						appendHeld()
						register()
					case "after removal, unregistered descriptor":
						appendHeld()
					case "after removal, recreated by path":
						recreated, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
						if openErr != nil {
							t.Fatalf("writer could not recreate the transcript: %v", openErr)
						}
						_, writeErr := recreated.WriteString(`{"type":"assistant","message":"resumed writer"}` + "\n")
						if err := errors.Join(writeErr, recreated.Close()); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				}
				trigger := 1 // Killed mode reads the clock first while backing up sidecars.
				if subagent {
					trigger = 2 // Subagent mode reads it first for the age gate.
				}
				calls := 0
				kills := &fakeKills{}
				if !subagent {
					kills.rows = []KilledChat{{ID: id, Engine: pfmengine.Claude}}
				}
				runner, err := New(Dependencies{
					Paths: values, Kills: kills, Proc: emptyProc{},
					Now: func() time.Time {
						calls++
						if calls == trigger && schedule == "after the live snapshot" {
							started = true
							open()
							appendHeld()
							register()
						}
						return time.Now()
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				report, err := runner.Run(
					context.Background(),
					Options{Apply: true, Subagents: subagent, OlderThan: 48 * time.Hour},
				)
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if len(kills.unkilled) != 0 {
					t.Errorf("archive retired the kill of a chat whose writer started: %v", kills.unkilled)
				}
				if schedule == "parent session live at the snapshot" {
					if len(report.Moves) != 0 || len(report.Live) != 1 {
						t.Errorf("a subagent transcript of a live session was planned: %+v", report)
					}
					if content, err := os.ReadFile(path); err != nil || string(content) != initial {
						t.Errorf("a live session's subagent transcript moved: content=%q error=%v", content, err)
					}
					return
				}
				if len(report.Moves) != 1 || report.Moves[0].Applied || report.Moves[0].Failed == "" {
					t.Fatalf("a relocation with a started writer was reported as done: %+v", report.Moves)
				}
				move := report.Moves[0]
				if schedule == "after removal, recreated by path" {
					// Both halves stay on disk and the report names them both.
					archived, archivedErr := os.ReadFile(move.Target)
					recreated, recreatedErr := os.ReadFile(path)
					if archivedErr != nil || string(archived) != initial ||
						recreatedErr != nil || !strings.Contains(string(recreated), "resumed writer") {
						t.Errorf("a recreated transcript lost bytes: archived=%q (%v) recreated=%q (%v)",
							archived, archivedErr, recreated, recreatedErr)
					}
					if !strings.Contains(move.Failed, move.Target) || !strings.Contains(move.Failed, path) {
						t.Errorf("the report does not name both kept copies: %q", move.Failed)
					}
					return
				}
				if writer != nil {
					if _, err := writer.WriteString(
						`{"type":"assistant","message":"still writing"}` + "\n",
					); err != nil {
						t.Fatal(err)
					}
				}
				content, err := os.ReadFile(path)
				if err != nil || !strings.HasPrefix(string(content), initial) ||
					!strings.Contains(string(content), "resumed writer") ||
					(writer != nil && !strings.Contains(string(content), "still writing")) {
					t.Errorf("archive relocated a transcript with a started writer: content=%q error=%v report=%+v",
						content, err, report)
				}
				if _, err := os.Lstat(move.Target); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a withdrawn relocation left its archive copy at %s: %v", move.Target, err)
				}
			})
		}
	}
}

// Two runs racing for one archive name: the loser's intent is the newer
// manifest row, but only the winner's publication is on disk, so only the
// winner may be restored from it — and to the winner's own original.
func TestArchiveLosingIntentNeverOwnsAnotherPublication(t *testing.T) {
	values := archiveJail(t)
	const id = "66666666-6666-4666-8666-666666666666"
	winner := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
	loser := filepath.Join(values.Home, "elsewhere", "-p", id+".jsonl")
	writeFile(t, winner, "winner\n")
	writeFile(t, loser, "loser\n")
	runner, err := New(Dependencies{Paths: values, Kills: &fakeKills{}, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	target := runner.targetFor(pfmengine.Claude, winner)
	if other := runner.targetFor(pfmengine.Claude, loser); other != target {
		t.Fatalf("fixture sources do not share one archive name: %s, %s", target, other)
	}
	first := Move{ID: id, Engine: "cc", Source: winner, Target: target, Bytes: 7}
	second := Move{ID: id, Engine: "cc", Source: loser, Target: target, Bytes: 6}
	originalLink := linkArchiveFile
	t.Cleanup(func() { linkArchiveFile = originalLink })
	var lost []Move
	linkArchiveFile = func(source, target string) error {
		switch source {
		case winner:
			// The first run has recorded its intent; the second run records
			// its own, then loses the race to the first run's publication.
			lost = runner.performMoves([]Move{second})
			return nil
		case loser:
			if err := originalLink(winner, target); err != nil {
				return err
			}
		}
		return originalLink(source, target)
	}
	won := runner.performMoves([]Move{first})
	if len(won) != 1 || !won[0].Applied || len(lost) != 1 || lost[0].Applied || lost[0].Failed == "" {
		t.Fatalf("fixture did not reach the lost race: won=%+v lost=%+v", won, lost)
	}
	row, err := Restore(values.ArchiveDir, id)
	if err != nil || row.Original != winner {
		t.Errorf("losing intent claimed winner publication: original=%s error=%v want=%s", row.Original, err, winner)
	}
	if content, err := os.ReadFile(winner); err != nil || string(content) != "winner\n" {
		t.Errorf("the winner's transcript did not come back: content=%q error=%v", content, err)
	}
	if content, err := os.ReadFile(loser); err != nil || string(content) != "loser\n" {
		t.Errorf("the loser's transcript changed: content=%q error=%v", content, err)
	}
}

// The publication is durable, then the source refuses to go: both names hold
// the transcript. A retry must finish the relocation and a restore must undo
// it; neither may stall on the copy this very transaction published.
func TestArchivePublishedMoveFinishesAfterSourceRemovalFails(t *testing.T) {
	for _, recovery := range []string{"retry", "restore"} {
		t.Run(recovery, func(t *testing.T) {
			values := archiveJail(t)
			const id = "77777777-7777-4777-8777-777777777777"
			source := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
			writeFile(t, source, "original\n")
			kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: pfmengine.Claude}}}
			runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			originalRemove := removeArchiveSource
			t.Cleanup(func() { removeArchiveSource = originalRemove })
			removeArchiveSource = func(string) error { return syscall.EACCES }
			first, err := runner.Run(context.Background(), Options{Apply: true})
			removeArchiveSource = originalRemove
			if err != nil || len(first.Moves) != 1 || first.Moves[0].Applied ||
				!strings.Contains(first.Moves[0].Failed, syscall.EACCES.Error()) || len(kills.unkilled) != 0 {
				t.Fatalf("fixture did not fail source removal after publication: report=%+v error=%v", first, err)
			}
			target := first.Moves[0].Target
			for _, path := range []string{source, target} {
				if content, err := os.ReadFile(path); err != nil || string(content) != "original\n" {
					t.Fatalf("the failed removal lost bytes at %s: content=%q error=%v", path, content, err)
				}
			}
			if recovery == "retry" {
				retry, err := runner.Run(context.Background(), Options{Apply: true})
				if err != nil || len(retry.Moves) != 1 || !retry.Moves[0].Applied || len(kills.unkilled) != 1 {
					t.Errorf("published move retry blocked after transient unlink failure: report=%+v error=%v",
						retry, err)
				}
				if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the retried relocation kept its source: %v", err)
				}
				if content, err := os.ReadFile(target); err != nil || string(content) != "original\n" {
					t.Errorf("the retried relocation lost its archive: content=%q error=%v", content, err)
				}
				return
			}
			if _, err := Restore(values.ArchiveDir, id); err != nil {
				t.Errorf("published move restore blocked after transient unlink failure: %v", err)
			}
			if content, err := os.ReadFile(source); err != nil || string(content) != "original\n" {
				t.Errorf("restore lost the transcript: content=%q error=%v", content, err)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("restore left the archive name behind: %v", err)
			}
		})
	}
}

// Across filesystems only a copy could move a transcript, and removing the
// source would strand any writer's descriptor on a deleted inode. The run
// refuses the move: the writer keeps appending to the transcript it holds, no
// archive name is taken, the kill stays, and the next same-filesystem run
// archives every byte.
func TestArchiveRefusesACrossFilesystemRelocation(t *testing.T) {
	values := archiveJail(t)
	const id = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	source := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
	writeFile(t, source, "initial\n")
	writer, err := os.OpenFile(source, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	originalLink := linkArchiveFile
	t.Cleanup(func() { linkArchiveFile = originalLink })
	linkArchiveFile = func(string, string) error { return syscall.EXDEV }
	kills := &fakeKills{rows: []KilledChat{{ID: id, Engine: pfmengine.Claude}}}
	runner, err := New(Dependencies{Paths: values, Kills: kills, Proc: emptyProc{}})
	if err != nil {
		t.Fatal(err)
	}
	refused, err := runner.Run(context.Background(), Options{Apply: true})
	if _, writeErr := writer.WriteString("resumed writer\n"); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil || len(refused.Moves) != 1 || refused.Moves[0].Applied ||
		!strings.Contains(refused.Moves[0].Failed, "another filesystem") {
		t.Fatalf("a cross-filesystem relocation was not refused: report=%+v error=%v", refused, err)
	}
	if len(kills.unkilled) != 0 {
		t.Errorf("a refused relocation retired the kill: %v", kills.unkilled)
	}
	if content, err := os.ReadFile(source); err != nil || string(content) != "initial\nresumed writer\n" {
		t.Errorf("the writer's append missed the transcript it holds: content=%q error=%v", content, err)
	}
	if _, err := os.Lstat(refused.Moves[0].Target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused relocation took the archive name: %v", err)
	}
	linkArchiveFile = originalLink
	retry, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil || len(retry.Moves) != 1 || !retry.Moves[0].Applied || len(kills.unkilled) != 1 {
		t.Fatalf("the refusal blocked a later same-filesystem run: report=%+v error=%v", retry, err)
	}
	if content, err := os.ReadFile(retry.Moves[0].Target); err != nil ||
		string(content) != "initial\nresumed writer\n" {
		t.Errorf("the archived transcript lost bytes: content=%q error=%v", content, err)
	}
}

// An append torn by a crash leaves a short row. It owns no file at its archived
// name, and the next row still lands on a line of its own.
func TestArchiveTornManifestRowOwnsNothing(t *testing.T) {
	values := archiveJail(t)
	const torn, next = "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	original := filepath.Join(values.Home, "-p", torn+".jsonl")
	archived := filepath.Join(values.ArchiveDir, "cc", "-p", torn+".jsonl")
	writeFile(t, archived, "someone else's file\n")
	writeFile(t, ManifestPath(values.ArchiveDir), manifestHeader+"\n"+
		strings.Join([]string{torn, "cc", original, "20", archived, "20261010-120000"}, "\t"))
	if _, err := Restore(values.ArchiveDir, torn); err == nil {
		t.Errorf("a torn row claimed the file at %s", archived)
	}
	if _, err := os.Lstat(original); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a torn row restored a file it never published: %v", err)
	}
	move := Move{ID: next, Engine: "cc", Source: "/next", Target: "/archived-next", fileID: "1:2"}
	if err := AppendManifest(values.ArchiveDir, move, "20261010-120001"); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadManifest(values.ArchiveDir)
	if err != nil || len(rows) != 1 || rows[0].ID != next || rows[0].FileID != "1:2" {
		t.Errorf("the row after a torn append was lost or misread: rows=%+v error=%v", rows, err)
	}
}
