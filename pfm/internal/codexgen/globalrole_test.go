package codexgen

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestReadGlobalRoleFileRefusesASymlinkTheWayCodexDoes pins the assumption
// this whole change rests on: Codex opens a role with O_NOFOLLOW, so a symlink
// is not readable as a role even when its target is a perfectly good TOML. A
// reader that quietly followed the link would certify a role every spawn
// rejects — and the three outcomes stay distinct, because "Codex will not load
// this", "nothing is here" and "the bytes are these" are three different
// findings.
func TestReadGlobalRoleFileRefusesASymlinkTheWayCodexDoes(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "real.toml")
	writeTestFile(t, plain, "name = \"real\"\n")
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}

	got, err := ReadGlobalRoleFile(plain)
	if err != nil {
		t.Fatalf("ReadGlobalRoleFile(regular file): %v", err)
	}
	if string(got) != "name = \"real\"\n" {
		t.Fatalf("read %q, want the file's own bytes", got)
	}

	if _, err := ReadGlobalRoleFile(link); !errors.Is(err, ErrGlobalRoleNotRegular) {
		t.Fatalf("ReadGlobalRoleFile(symlink) err=%v, want ErrGlobalRoleNotRegular", err)
	}
	if _, err := ReadGlobalRoleFile(dir); !errors.Is(err, ErrGlobalRoleNotRegular) {
		t.Fatalf("ReadGlobalRoleFile(directory) err=%v, want ErrGlobalRoleNotRegular", err)
	}
	if _, err := ReadGlobalRoleFile(filepath.Join(dir, "absent.toml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadGlobalRoleFile(absent) err=%v, want fs.ErrNotExist — absence is not a refusal", err)
	}
}

// TestClassifyGlobalRoleSeparatesOursFromTheirs walks every state the install
// path can meet at a role's path. The line that matters most is the foreign
// one: a regular file with no generated marker belongs to the operator, and
// ApplyGlobalRole must leave it byte-for-byte alone whatever its name.
func TestClassifyGlobalRoleSeparatesOursFromTheirs(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy")
	want := []byte(globalRoleHeader("templates/global/agents/alpha.md") + "name = \"alpha\"\n")

	cases := []struct {
		name  string
		setup func(target string)
		state GlobalRoleState
	}{
		{"missing", func(string) {}, GlobalRoleMissing},
		{"current", func(target string) { writeTestFile(t, target, string(want)) }, GlobalRoleCurrent},
		{
			"stale",
			func(target string) {
				writeTestFile(t, target, globalRoleHeader("templates/global/agents/alpha.md")+"name = \"old\"\n")
			},
			GlobalRoleStale,
		},
		{"owned-link", func(target string) {
			source := filepath.Join(legacy, "alpha.toml")
			writeTestFile(t, source, "name = \"alpha\"\n")
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
		}, GlobalRoleOwnedLink},
		{"foreign file", func(target string) {
			writeTestFile(t, target, "name = \"alpha\"\ndescription = \"the operator's own\"\n")
		}, GlobalRoleForeign},
		{"foreign link", func(target string) {
			source := filepath.Join(dir, "elsewhere.toml")
			writeTestFile(t, source, "name = \"alpha\"\n")
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
		}, GlobalRoleForeign},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "alpha.toml")
			testCase.setup(target)
			before, _ := os.ReadFile(target)

			state, _, err := ClassifyGlobalRole(target, want, []string{legacy})
			if err != nil {
				t.Fatalf("ClassifyGlobalRole: %v", err)
			}
			if state != testCase.state {
				t.Fatalf("state=%s, want %s", state, testCase.state)
			}

			if err := ApplyGlobalRole(target, want, state); err != nil {
				t.Fatalf("ApplyGlobalRole: %v", err)
			}
			if state == GlobalRoleForeign {
				// os.ReadFile, not ReadGlobalRoleFile: a foreign entry may be
				// the very symlink Codex refuses, and the question here is
				// only whether apply left it exactly as the operator had it.
				after, afterErr := os.ReadFile(target)
				if afterErr != nil || !bytes.Equal(after, before) {
					t.Fatalf("a foreign role was touched: read %q err=%v, want %q", after, afterErr, before)
				}
				return
			}
			got, err := ReadGlobalRoleFile(target)
			if err != nil {
				t.Fatalf("after apply, Codex cannot read the role: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("after apply, role = %q, want %q", got, want)
			}
		})
	}
}

// TestGeneratedGlobalRoleAnchorsOnTheFirstLine pins the ownership proof to
// where pfm writes it. A body that merely quotes the marker phrase is not a
// file pfm owns, and treating it as one would hand an operator's role to the
// retire path.
func TestGeneratedGlobalRoleAnchorsOnTheFirstLine(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
		owned   bool
	}{
		{"today's header", globalRoleHeader("templates/global/agents/alpha.md"), true},
		{"the retired build-codex.mjs header", "# " + legacyGeneratedMarker + " from x\nname = \"a\"\n", true},
		{"no marker", "name = \"a\"\n", false},
		{"the phrase buried in a body", "name = \"a\"\ndeveloper_instructions = \"\"\"\n" + generatedMarker + "\n", false},
		{"empty", "", false},
	} {
		if got := GeneratedGlobalRole([]byte(testCase.content)); got != testCase.owned {
			t.Errorf("GeneratedGlobalRole(%s) = %v, want %v", testCase.name, got, testCase.owned)
		}
	}
}
