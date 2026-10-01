package cmdparse

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// corpusFixture holds real Bash commands from Claude sessions,
// each with the parse it must yield. Every path in it lies under corpusRoot.
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
	// Cwd is the call's directory, under corpusRoot; the parse never reads it.
	Cwd     string       `json:"cwd"`
	Command string       `json:"command"`
	Parts   []corpusPart `json:"parts"`
	// KnownDefect names a parser defect against the spec: the case skips
	// while the parse still differs, and fails once it matches.
	KnownDefect string `json:"known_defect,omitempty"`
}

type corpusPart struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Status  string   `json:"status"`
	Bounded bool     `json:"bounded,omitempty"`
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

// asCorpus renders parsed parts in the fixture's terms, no arguments read
// as an empty list.
func asCorpus(parts []Part) []corpusPart {
	out := make([]corpusPart, 0, len(parts))
	for _, p := range parts {
		args := p.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, corpusPart{Program: p.Program, Args: args, Status: p.Status, Bounded: p.Bounded})
	}
	return out
}

// TestRealCommandsCorpus parses every corpus command in one batch and
// compares each call's parts, programs, arguments, statuses and bounds with
// the fixture.
func TestRealCommandsCorpus(t *testing.T) {
	cases := loadCorpus(t)
	calls := make([]Call, len(cases))
	for i, c := range cases {
		calls[i] = Call{ID: fmt.Sprintf("corpus-%d", i), Command: c.Command, Cwd: c.Cwd}
	}
	got, err := ParseBatch(calls)
	if err != nil {
		t.Fatalf("ParseBatch over corpus %s: %v", corpusFixture, err)
	}
	for i, c := range cases {
		parts, ok := got[calls[i].ID]
		t.Run(c.Name, func(t *testing.T) {
			if !ok {
				t.Fatalf("ParseBatch returned no entry for call %s", calls[i].ID)
			}
			have := asCorpus(parts)
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
