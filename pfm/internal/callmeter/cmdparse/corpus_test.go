package cmdparse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// corpusFixture holds real Bash commands from Claude sessions,
// each with the parse the spec requires (docs/design/hooks/callmeter.md,
// "Test corpora" item 1). Every path in it is rewritten under corpusRoot.
const (
	corpusFixture = "testdata/commands-corpus.json"
	corpusRoot    = "/tmp/demo-proj"
	// corpusFloor fails the test when the fixture shrinks to a token set:
	// a corpus that asserts nothing must never read as a pass.
	corpusFloor = 40
)

type corpusCase struct {
	Name string `json:"name"`
	// Covers names the shape the case guards (heredoc, cd chain, wrapper…).
	Covers string `json:"covers"`
	// Cwd is the call's directory, under corpusRoot.
	Cwd string `json:"cwd"`
	// Dirs and Files are created under the case's root, relative to it; a
	// path a case names but does not list stays absent on purpose.
	Dirs    []string     `json:"dirs,omitempty"`
	Files   []string     `json:"files,omitempty"`
	Command string       `json:"command"`
	Parts   []corpusPart `json:"parts"`
	// KnownDefect names a parser defect against the spec: the case skips
	// while the parse still differs, and fails once it matches.
	KnownDefect string `json:"known_defect,omitempty"`
}

type corpusPart struct {
	Program     string       `json:"program"`
	Lang        string       `json:"lang"`
	Status      string       `json:"status"`
	Error       string       `json:"error,omitempty"`
	Conditional bool         `json:"conditional"`
	Files       []corpusFile `json:"files,omitempty"`
}

type corpusFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Range  string `json:"range,omitempty"`
	Exists bool   `json:"exists"`
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(corpusFixture)
	if err != nil {
		t.Fatalf("read corpus %s: %v", corpusFixture, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var cases []corpusCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatalf("decode corpus %s: %v", corpusFixture, err)
	}
	if len(cases) < corpusFloor {
		t.Fatalf("corpus %s holds %d cases, want at least %d", corpusFixture, len(cases), corpusFloor)
	}
	seen := map[string]bool{}
	for i := range cases {
		c := &cases[i]
		switch {
		case c.Name == "" || c.Command == "":
			t.Fatalf("corpus %s case %d: name and command are required", corpusFixture, i)
		case seen[c.Name]:
			t.Fatalf("corpus %s: duplicate case name %q", corpusFixture, c.Name)
		case c.Cwd != corpusRoot && !strings.HasPrefix(c.Cwd, corpusRoot+"/"):
			t.Fatalf("corpus %s case %q: cwd %q is not under %s", corpusFixture, c.Name, c.Cwd, corpusRoot)
		}
		seen[c.Name] = true
	}
	return cases
}

// materialize creates the case's tree under a fresh root and returns it.
func materialize(t *testing.T, c corpusCase) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range append(c.Dirs, strings.TrimPrefix(strings.TrimPrefix(c.Cwd, corpusRoot), "/")) {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatalf("case %q: mkdir %s: %v", c.Name, d, err)
		}
	}
	for _, f := range c.Files {
		path := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("case %q: mkdir for %s: %v", c.Name, f, err)
		}
		if err := os.WriteFile(path, []byte("line\n"), 0o600); err != nil {
			t.Fatalf("case %q: write %s: %v", c.Name, f, err)
		}
	}
	return root
}

// asCorpus renders parsed parts in the fixture's terms, the case's root
// rewritten back to corpusRoot.
func asCorpus(parts []Part, root string) []corpusPart {
	back := func(s string) string { return strings.ReplaceAll(s, root, corpusRoot) }
	out := make([]corpusPart, 0, len(parts))
	for i := range parts {
		p := &parts[i]
		cp := corpusPart{
			Program:     back(p.Program),
			Lang:        p.Lang,
			Status:      p.Status,
			Error:       back(p.Error),
			Conditional: p.Conditional,
		}
		for _, f := range p.Files {
			cp.Files = append(
				cp.Files,
				corpusFile{Path: back(f.Path), Action: f.Action, Range: f.Range, Exists: f.Exists},
			)
		}
		out = append(out, cp)
	}
	return out
}

// TestRealCommandsCorpus parses every corpus command in one batch, as a
// report run does, through the real python3 (its absence fails the test),
// and compares each call's parts, files, actions, ranges, exists and
// conditional flags with the fixture.
func TestRealCommandsCorpus(t *testing.T) {
	cases := loadCorpus(t)
	roots := make([]string, len(cases))
	calls := make([]Call, len(cases))
	for i, c := range cases {
		roots[i] = materialize(t, c)
		calls[i] = Call{
			ID:      fmt.Sprintf("corpus-%d", i),
			Command: strings.ReplaceAll(c.Command, corpusRoot, roots[i]),
			Cwd:     strings.Replace(c.Cwd, corpusRoot, roots[i], 1),
		}
	}
	got, err := ParseBatch(context.Background(), calls, Python3{Runner: deps.RealRunner{}})
	if err != nil {
		t.Fatalf("ParseBatch over corpus %s: %v", corpusFixture, err)
	}
	for i, c := range cases {
		parts, ok := got[calls[i].ID]
		t.Run(c.Name, func(t *testing.T) {
			if !ok {
				t.Fatalf("ParseBatch returned no entry for call %s", calls[i].ID)
			}
			have := asCorpus(parts, roots[i])
			want := c.Parts
			if want == nil {
				want = []corpusPart{}
			}
			if reflect.DeepEqual(have, want) {
				if c.KnownDefect != "" {
					t.Fatalf("known defect %q no longer reproduces: drop known_defect from the case", c.KnownDefect)
				}
				return
			}
			if c.KnownDefect != "" {
				t.Skipf("known defect: %s", c.KnownDefect)
			}
			gotJSON, err := json.MarshalIndent(have, "", "  ")
			if err != nil {
				t.Fatalf("marshal parts: %v", err)
			}
			wantJSON, err := json.MarshalIndent(want, "", "  ")
			if err != nil {
				t.Fatalf("marshal want: %v", err)
			}
			t.Errorf("command:\n%s\nparts:\n%s\nwant:\n%s", c.Command, gotJSON, wantJSON)
		})
	}
}
