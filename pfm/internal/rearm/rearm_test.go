package rearm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/agentrole"
)

// mustWriteFile is the one filesystem primitive every test below builds its
// jail out of. Every path lives under t.TempDir(): never the real fleet,
// never $HOME.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPointerThresholdSplit pins behaviour 2: a constitution at or under the
// passed threshold re-arms with its FULL text; above it, Pointer switches to
// a short pointer that names the artifact — and, for a TOML seat, the exact
// developer_instructions key agentrole.ReadArtifact reads out of it, never a
// literal duplicating that string.
func TestPointerThresholdSplit(t *testing.T) {
	t.Run("at the threshold (equal, not just under) returns the full constitution text", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dev.md")
		body := "UNIQUE FULL BODY MARKER, exactly at the threshold.\n"
		mustWriteFile(t, path, body)
		crumb := Crumb{Role: "dev", ArtifactPath: path, TOMLKey: false}

		got := Pointer(crumb, len(body)) // threshold == len(text) exactly
		if !strings.Contains(got, "UNIQUE FULL BODY MARKER") {
			t.Fatalf("Pointer() = %q, want the full body at the threshold boundary", got)
		}
		if strings.Contains(got, "read "+path+" in full") {
			t.Fatalf("Pointer() = %q, a full-text re-arm must not also carry the short-pointer phrasing", got)
		}
	})

	t.Run("above threshold returns a short pointer naming the artifact", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "reviewer.md")
		body := "UNIQUE FULL BODY MARKER THAT MUST NOT LEAK INTO THE POINTER.\n"
		mustWriteFile(t, path, body)
		crumb := Crumb{Role: "reviewer", ArtifactPath: path, TOMLKey: false}

		got := Pointer(crumb, len(body)-1) // one byte under the body's own length
		if !strings.Contains(got, path) {
			t.Fatalf("Pointer() = %q, want it to name the artifact path %q", got, path)
		}
		if strings.Contains(got, "UNIQUE FULL BODY MARKER") {
			t.Fatalf("Pointer() = %q, the full body leaked into a short pointer", got)
		}
	})

	t.Run(
		"a TOML artifact's pointer names the developer_instructions key, never a hardcoded literal",
		func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "reviewer.toml")
			body := strings.Repeat("x", 80)
			mustWriteFile(t, path, "name = \"reviewer\"\ndeveloper_instructions = \"\"\"\n"+body+"\n\"\"\"\n")
			crumb := Crumb{Role: "reviewer", ArtifactPath: path, TOMLKey: true}

			// A threshold well under the body's own length forces the short
			// pointer branch without depending on TOML's exact leading/trailing
			// newline trimming rules.
			got := Pointer(crumb, 10)
			if !strings.Contains(got, agentrole.DeveloperInstructionsKey) {
				t.Fatalf(
					"Pointer() = %q, want it to name agentrole.DeveloperInstructionsKey (%q)",
					got,
					agentrole.DeveloperInstructionsKey,
				)
			}
			if !strings.Contains(got, path) {
				t.Fatalf("Pointer() = %q, want it to also name the artifact path %q", got, path)
			}
		},
	)
}

// TestPointerVanishedArtifactFailsVisibly pins behaviour 4: an artifact that
// vanished between birth and re-arm must render as a non-empty, VISIBLE
// failure naming the role — never an empty string, and never text that
// reads like a successful re-arm.
func TestPointerVanishedArtifactFailsVisibly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.md")
	mustWriteFile(t, path, "will be deleted\n")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture artifact: %v", err)
	}
	crumb := Crumb{Role: "dev", ArtifactPath: path, TOMLKey: false}

	got := Pointer(crumb, DefaultThresholdBytes)
	if got == "" {
		t.Fatal("Pointer() = \"\", want a visible failure for a vanished artifact")
	}
	if !strings.Contains(got, "dev") {
		t.Fatalf("Pointer() = %q, want it to name the role %q", got, "dev")
	}
	if strings.Contains(got, "you are still dev — re-armed") {
		t.Fatalf("Pointer() = %q, a vanished artifact must not render as a successful re-arm", got)
	}
}

// TestReadCrumbThreeStatesNeverCollapse pins behaviour 5: ReadCrumb's three
// return states stay distinct. A test asserting only (a) and (c) proves
// nothing about the one that matters here — (b), a crumb file that IS there
// but cannot be parsed.
func TestReadCrumbThreeStatesNeverCollapse(t *testing.T) {
	t.Run("no crumb file at either path returns the zero value, false, nil", func(t *testing.T) {
		sidDir := t.TempDir()
		crumb, ok, err := ReadCrumb(sidDir, "cc-1800000000-1-1", "")
		if err != nil {
			t.Fatalf("ReadCrumb() error = %v, want nil for no crumb file", err)
		}
		if ok {
			t.Fatal("ReadCrumb() ok = true, want false for no crumb file")
		}
		if crumb != (Crumb{}) {
			t.Fatalf("ReadCrumb() crumb = %#v, want the zero value", crumb)
		}
	})

	t.Run(
		"a crumb file that exists but cannot be parsed is a real error, never folded into absence",
		func(t *testing.T) {
			sidDir := t.TempDir()
			socket := "cc-1800000000-1-2"
			path := filepath.Join(sidDir, "role-"+socket)
			mustWriteFile(t, path, "{ this is not valid json")

			crumb, ok, err := ReadCrumb(sidDir, socket, "")
			if err == nil {
				t.Fatal("ReadCrumb() error = nil, want a parse error for a corrupt crumb file")
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("ReadCrumb() error = %q, want it to name the path %q", err.Error(), path)
			}
			if ok {
				t.Fatal("ReadCrumb() ok = true alongside a parse error")
			}
			if crumb != (Crumb{}) {
				t.Fatalf("ReadCrumb() crumb = %#v, want the zero value alongside an error", crumb)
			}
		},
	)

	t.Run("a live crumb returns it, true, nil", func(t *testing.T) {
		sidDir := t.TempDir()
		socket := "cc-1800000000-1-3"
		artifact := filepath.Join(t.TempDir(), "dev.md")
		mustWriteFile(t, artifact, "constitution\n")
		want := Crumb{Role: "dev", ArtifactPath: artifact, TOMLKey: false}
		if err := WriteCrumb(sidDir, socket, want); err != nil {
			t.Fatalf("WriteCrumb() error = %v", err)
		}

		got, ok, err := ReadCrumb(sidDir, socket, "")
		if err != nil {
			t.Fatalf("ReadCrumb() error = %v, want nil for a live crumb", err)
		}
		if !ok {
			t.Fatal("ReadCrumb() ok = false, want true for a live crumb")
		}
		if got != want {
			t.Fatalf("ReadCrumb() = %#v, want %#v", got, want)
		}
	})
}

// TestCrumbLifecycleWriteThenRemove pins behaviour 6's package-level half:
// WriteCrumb then RemoveCrumb removes it, and RemoveCrumb on an
// already-absent crumb is not an error (the ordinary case for a seat that
// never had --role, or a socket already cleaned up).
func TestCrumbLifecycleWriteThenRemove(t *testing.T) {
	sidDir := t.TempDir()
	socket := "cc-1800000000-1-4"
	artifact := filepath.Join(t.TempDir(), "dev.md")
	mustWriteFile(t, artifact, "constitution\n")
	crumb := Crumb{Role: "dev", ArtifactPath: artifact, TOMLKey: false}

	if err := WriteCrumb(sidDir, socket, crumb); err != nil {
		t.Fatalf("WriteCrumb() error = %v", err)
	}
	if _, ok, err := ReadCrumb(sidDir, socket, ""); err != nil || !ok {
		t.Fatalf("ReadCrumb() after WriteCrumb = (ok=%t, err=%v), want a live crumb", ok, err)
	}

	if err := RemoveCrumb(sidDir, socket, ""); err != nil {
		t.Fatalf("RemoveCrumb() error = %v, want nil for a real crumb", err)
	}
	if _, ok, err := ReadCrumb(sidDir, socket, ""); err != nil || ok {
		t.Fatalf("ReadCrumb() after RemoveCrumb = (ok=%t, err=%v), want no crumb", ok, err)
	}

	if err := RemoveCrumb(sidDir, socket, ""); err != nil {
		t.Fatalf("RemoveCrumb() on an already-absent crumb error = %v, want nil", err)
	}
}
