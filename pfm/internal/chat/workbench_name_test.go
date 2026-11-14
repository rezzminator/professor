package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func chatWorkbenchFixture(t *testing.T, root string) string {
	t.Helper()
	project := filepath.Join(root, "acme")
	dir := filepath.Join(project, "docs", "scribe")
	for path, body := range map[string]string{
		professor.BaselinePath(project):               "{}",
		paths.WorkbenchManifest(dir):                  `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"): "You are scribe.",
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWorkbenchNameOutside(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	if name, found, err := WorkbenchName(
		context.Background(),
		dir,
		io.Discard,
		nil,
	); err != nil || !found ||
		name != "_SCRIBE:1" {
		t.Fatalf("inside control = %q, %t, %v", name, found, err)
	}
	name, found, err := WorkbenchName(context.Background(), filepath.Join(root, "acme", "src"), io.Discard, nil)
	if err != nil || found || name != "" {
		t.Fatalf("outside = %q, %t, %v", name, found, err)
	}
}

func TestWorkbenchNameReservedNames(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	name, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil, "_SCRIBE:1", "_SCRIBE:3")
	if err != nil || !found || name != "_SCRIBE:2" {
		t.Fatalf("WorkbenchName with reserved names = %q, %t, %v; want _SCRIBE:2, true, nil", name, found, err)
	}
}

func TestWorkbenchNameReportsRosterFailure(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	blocker := filepath.Join(root, "blocker")
	if err := atomicfile.Write(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
	_, rosterErr := Rows(context.Background(), io.Discard, nil)
	name, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err == nil || rosterErr == nil || err.Error() != rosterErr.Error() || found || name != "" {
		t.Fatalf("failed roster = %q, %t, %v; want %v", name, found, err, rosterErr)
	}
}

func TestWorkbenchNameReportsInvalidManifest(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	if err := atomicfile.Write(paths.WorkbenchManifest(dir), []byte(`{"prompt":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	name, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	want := paths.WorkbenchManifest(dir) + `: "prompt" is required`
	if err == nil || err.Error() != want || name != "" || found {
		t.Fatalf("invalid name = %q, %t, %v; want %q", name, found, err, want)
	}
}

func TestWorkbenchNameClaimsAcrossIndependentProducers(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	first, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || !found {
		t.Fatal(first, found, err)
	}
	second, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || !found || second == first {
		t.Fatalf("separate producers chose %q and %q: %v", first, second, err)
	}
	// A claim pfm cannot read keeps its number and the next one is chosen.
	claims := filepath.Join(root, "sid", ".name-claims")
	broken := filepath.Join(claims, fmt.Sprintf("%x", sha256.Sum256([]byte("_SCRIBE:3"))))
	if err := atomicfile.Write(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	third, found, err := WorkbenchName(context.Background(), dir, &warn, nil)
	wantWarn := "pfm WARN: parse workbench claim " + broken +
		": unexpected end of JSON input; _SCRIBE:3 treated as taken\n"
	if err != nil || !found || third != "_SCRIBE:4" || warn.String() != wantWarn {
		t.Fatalf("past an unreadable claim chose %q, %t, %v; warned %q, want _SCRIBE:4 and %q",
			third, found, err, warn.String(), wantWarn)
	}
}

func TestWorkbenchNameSurvivesPostCreationFailure(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	claim, found, err := ReserveWorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || !found {
		t.Fatal(claim, found, err)
	}
	launchErr := errors.New("chat born but boot probe failed")
	if err := CommitWorkbenchLaunch(
		WithWorkbenchNameReservation(context.Background(), claim),
		true,
		claim.Name,
		launchErr,
	); !errors.Is(
		err,
		launchErr,
	) {
		t.Fatalf("launch error lost: %v", err)
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	next, _, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || next == claim.Name {
		t.Fatalf("live but unnamed chat lost reservation: %q %v", next, err)
	}
}

func TestWorkbenchStaleReleasePreservesReplacementClaim(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	first, _, err := ReserveWorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || replacement != first.Name {
		t.Fatal(replacement, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err == nil {
		t.Fatal("stale capability committed another producer claim")
	}
	next, _, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || next == replacement {
		t.Fatalf("stale release removed replacement claim: next=%q err=%v", next, err)
	}
}
