package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestWalkClaudeRootsUsesGivenSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "p")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "x.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, link := range []string{a, b} {
		if err := os.Symlink(filepath.Dir(shared), link); err != nil {
			t.Fatal(err)
		}
	}
	files, complete, err := walkClaudeRoots(context.Background(), []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(a, "p", "x.jsonl"), filepath.Join(b, "p", "x.jsonl")}
	if !complete || len(files) != 2 || files[0].Path != want[0] || files[1].Path != want[1] {
		t.Fatalf("paths = %v complete=%t, want %v complete", files, complete, want)
	}
}

// pathMax is Linux's PATH_MAX: a path this long or longer is refused to every
// user, root included, so it stands in for an unreadable entry in the fence.
const pathMax = 4096

// overlongClaudeRoot builds a Claude root of exactly rootLen characters
// holding p/good.jsonl, by renaming a short last component long once its
// children exist, so a child may end up past pathMax.
func overlongClaudeRoot(t *testing.T, rootLen int, children func(root string)) (root, good string) {
	t.Helper()
	// parent is padded to rootLen-201, so the rename adds 199 characters.
	parent := t.TempDir()
	for remaining := rootLen - 201 - len(parent); remaining > 0; remaining = rootLen - 201 - len(parent) {
		size := min(200, remaining-1)
		if remaining-size-1 == 1 { // a 1-character remainder cannot be a component
			size--
		}
		parent = filepath.Join(parent, strings.Repeat("d", size))
	}
	short := filepath.Join(parent, "r")
	if err := os.MkdirAll(short, 0o700); err != nil {
		t.Fatal(err)
	}
	children(short)
	writeLines(t, filepath.Join(short, "p", "good.jsonl"), `{"type":"user"}`)
	root = filepath.Join(parent, strings.Repeat("r", 200))
	if err := os.Rename(short, root); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "p", "good.jsonl")
}

// TestWalkClaudeRootsSkipsWhatItCannotRead: a root, project directory or
// transcript the kernel refuses costs that item only; the walk lists the rest
// and reports itself incomplete, so the pass prunes nothing.
func TestWalkClaudeRootsSkipsWhatItCannotRead(t *testing.T) {
	longName := strings.Repeat("x", 250)
	for _, test := range []struct {
		name     string
		rootLen  int
		children func(root string)
		unread   func(root string) string
		wantKind string
		wantErr  func(path string) string
	}{
		{
			name:    "project directory",
			rootLen: pathMax - 106,
			children: func(root string) {
				if err := os.MkdirAll(filepath.Join(root, longName[:200]), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			unread:   func(root string) string { return filepath.Join(root, longName[:200]) },
			wantKind: "claude directory",
			wantErr:  func(path string) string { return "open " + path + ": file name too long" },
		},
		{
			name:    "transcript",
			rootLen: pathMax - 200,
			children: func(root string) {
				writeLines(t, filepath.Join(root, "q", longName[:240]+".jsonl"), `{"type":"user"}`)
			},
			unread:   func(root string) string { return filepath.Join(root, "q", longName[:240]+".jsonl") },
			wantKind: "claude transcript",
			wantErr:  func(path string) string { return "lstat " + path + ": file name too long" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, good := overlongClaudeRoot(t, test.rootLen, test.children)
			unread := test.unread(root)
			if len(root) >= pathMax || len(unread) < pathMax {
				t.Fatalf("fixture lengths root=%d unread=%d, want root < %d <= unread", len(root), len(unread), pathMax)
			}
			ctx, recorder := obs.Test(t)
			files, complete, err := walkClaudeRoots(ctx, []string{root})
			if err != nil {
				t.Fatalf("walkClaudeRoots() error = %v, want the walk to go on", err)
			}
			if complete || len(files) != 1 || files[0].Path != good {
				t.Fatalf("files = %v complete=%t, want only %s and incomplete", files, complete, good)
			}
			want := test.wantKind + "|" + obs.TruncateValue(test.wantErr(unread))
			if got := unreadableWarnings(recorder, obs.TruncateValue(unread)); len(got) != 1 || got[0] != want {
				t.Fatalf("warnings = %q, want [%q]", got, want)
			}
		})
	}
}

// TestWalkClaudeRootsSkipsAnUnreadableRoot: one account root that cannot be
// stat'ed (here a path through a file) leaves the other roots listed.
func TestWalkClaudeRootsSkipsAnUnreadableRoot(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(file, "root")
	good := filepath.Join(base, "good")
	writeLines(t, filepath.Join(good, "p", "x.jsonl"), `{"type":"user"}`)
	ctx, recorder := obs.Test(t)
	files, complete, err := walkClaudeRoots(ctx, []string{broken, good})
	if err != nil {
		t.Fatalf("walkClaudeRoots() error = %v, want the walk to go on", err)
	}
	if complete || len(files) != 1 || files[0].Path != filepath.Join(good, "p", "x.jsonl") {
		t.Fatalf("files = %v complete=%t, want only the good root's transcript and incomplete", files, complete)
	}
	want := "claude root|stat " + broken + ": not a directory"
	if got := unreadableWarnings(recorder, broken); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings = %q, want [%q]", got, want)
	}
}
