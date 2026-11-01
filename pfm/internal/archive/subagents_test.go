package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Subagent mode: sidechain transcripts archived by age, never a live one,
// and never a path the scan could not read.

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

// A path the sidechain scan cannot read costs only itself: it is reported
// unresolved with its cause, and the readable sidechain beside it is still
// archived. Each failure is one every user gets, root included.
func TestArchiveSubagentsReportsWhatItCouldNotRead(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, values *paths.Values, project string) (path, cause string)
	}{
		{
			name: "unreadable root",
			arrange: func(t *testing.T, values *paths.Values, _ string) (string, string) {
				blocker := filepath.Join(t.TempDir(), "file")
				writeFile(t, blocker, "not a directory\n")
				root := filepath.Join(blocker, "projects")
				values.Roots[pfmengine.Claude] = []string{root, values.Roots[pfmengine.Claude][0]}
				return root, "lstat " + root + ": not a directory"
			},
		},
		{
			name: "unstatable transcript",
			arrange: func(t *testing.T, _ *paths.Values, project string) (string, string) {
				// A directory just under PATH_MAX lists, but its entry's full
				// path is too long for lstat.
				directory := project
				for len(directory)+201 < 3900 {
					directory = filepath.Join(directory, strings.Repeat("d", 200))
				}
				directory = filepath.Join(directory, strings.Repeat("e", 3950-len(directory)-1))
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				root, err := os.OpenRoot(directory)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := root.Close(); err != nil {
						t.Fatal(err)
					}
				}()
				name := strings.Repeat("a", 240) + ".jsonl"
				file, err := root.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				path := directory + "/" + name
				return path, "lstat " + path + ": file name too long"
			},
		},
		{
			name: "unreadable head",
			arrange: func(t *testing.T, _ *paths.Values, project string) (string, string) {
				path := filepath.Join(project, "dddddddd-0000-4000-8000-000000000004.jsonl")
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
				return path, "read " + path + ": is a directory"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := archiveJail(t)
			project := filepath.Join(values.Roots[pfmengine.Claude][0], "-p")
			sidechain := filepath.Join(project, "aaaaaaaa-0000-4000-8000-000000000001.jsonl")
			writeFile(t, sidechain, `{"isSidechain":true,"type":"user"}`+"\n")
			path, cause := test.arrange(t, &values, project)
			runner, err := New(Dependencies{
				Paths: values,
				Kills: &fakeKills{},
				Proc:  emptyProc{},
				Now:   func() time.Time { return time.Now().Add(72 * time.Hour) },
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runner.Run(context.Background(), Options{Subagents: true, OlderThan: 48 * time.Hour})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(report.Moves) != 1 || report.Moves[0].Source != sidechain {
				t.Fatalf("moves = %#v, want only the readable sidechain %s", report.Moves, sidechain)
			}
			got, want := fmt.Sprint(report.UnresolvedErrors), fmt.Sprint(map[string]string{path: cause})
			if got != want {
				t.Fatalf("UnresolvedErrors = %s, want %s", got, want)
			}
		})
	}
}
