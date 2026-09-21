package picker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

func TestProfessorUpdatePromptExplainsThenAsksBeforeUpdating(t *testing.T) {
	prompt := professorUpdatePrompt(compose.Row{ID: "pfm-update-v0.61.2"})
	overview := strings.Index(prompt, "present a concise overview")
	approval := strings.Index(prompt, "Ask the user for explicit approval")
	update := strings.Index(prompt, "pfm update --to v0.61.2")
	if overview < 0 || approval < overview || update < approval {
		t.Fatalf("update prompt order is not overview → approval → update: %q", prompt)
	}
	// A banner can fire for an adopter several releases behind: reading only
	// the target's notes skips every intervening release's migration actions.
	for _, want := range []string{
		"pfm doctor", "Do not push, tag, publish, release", "Professor v0.61.2",
		"pfm version", "EVERY release-notes file after the installed version through v0.61.2",
		"git show v0.61.2:releases/vX.Y.Z.md", "#### → For:", "one checklist",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("update prompt %q lacks %q", prompt, want)
		}
	}
}

func releaseRuntime(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	return pfmconfig.Runtime{
		Version: "v1.0.0",
		Paths:   paths.Values{DB: filepath.Join(t.TempDir(), "fleet.db")},
	}
}

// releaseRuntimeWithSourceRepo is releaseRuntime plus a valid source-repo
// marker, the one extra fixture cachedProfessorUpdateFailureRow needs beyond
// professorUpdateCheckNotice's own (it renders a Row, which — like
// cachedProfessorUpdateRow — carries the repo's CWD/Project).
func releaseRuntimeWithSourceRepo(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	runtime := releaseRuntime(t)
	home := t.TempDir()
	repo := t.TempDir()
	if err := installer.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	runtime.Paths.Home = home
	return runtime
}

// TestProfessorUpdateCheckNoticeStaysSilentOutsideARelease and its siblings
// pin the three states cachedProfessorUpdateRow's own ("", false) answer
// cannot distinguish (Lane-2 §5): genuine absence, an unreadable cache, and
// a detached checker that has been failing every run.
func TestProfessorUpdateCheckNoticeStaysSilentOutsideARelease(t *testing.T) {
	runtime := releaseRuntime(t)
	runtime.Version = pfmconfig.DevelopmentVersion
	if notice := professorUpdateCheckNotice(runtime); notice != "" {
		t.Fatalf("notice = %q, want silence for a non-release build", notice)
	}
}

func TestProfessorUpdateCheckNoticeStaysSilentWithNoCacheAndNoFailure(t *testing.T) {
	runtime := releaseRuntime(t)
	if notice := professorUpdateCheckNotice(runtime); notice != "" {
		t.Fatalf("notice = %q, want silence: no cache and no failure marker is a genuine first run", notice)
	}
}

func TestProfessorUpdateCheckNoticeNamesAnUnreadableCache(t *testing.T) {
	runtime := releaseRuntime(t)
	cachePath := professorUpdateCachePath(runtime)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	notice := professorUpdateCheckNotice(runtime)
	if !strings.Contains(notice, "could not read") {
		t.Fatalf("notice = %q, want it to name the unreadable cache, not silence", notice)
	}
}

func TestProfessorUpdateCheckNoticeNamesAPersistentlyFailingChecker(t *testing.T) {
	runtime := releaseRuntime(t)
	cachePath := professorUpdateCachePath(runtime)
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), cachePath, runtime.Version, failing.URL, failing.Client(),
	); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}

	notice := professorUpdateCheckNotice(runtime)
	if !strings.Contains(notice, "failing since") || !strings.Contains(notice, "network") {
		t.Fatalf("notice = %q, want it to name the ongoing failure and its class", notice)
	}
}

// TestProfessorUpdateCheckNoticeStaysSilentWhenAnUpdateIsAlreadyRendered:
// cachedProfessorUpdateRow already surfaces a found update as its own picker
// row — a second stderr line would only repeat it.
func TestProfessorUpdateCheckNoticeStaysSilentWhenAnUpdateIsAlreadyRendered(t *testing.T) {
	runtime := releaseRuntime(t)
	cachePath := professorUpdateCachePath(runtime)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v1.2.0")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), cachePath, runtime.Version, server.URL, server.Client(),
	); err != nil {
		t.Fatal(err)
	}

	if notice := professorUpdateCheckNotice(runtime); notice != "" {
		t.Fatalf("notice = %q, want silence — the row itself already names the update", notice)
	}
}

// A detached checker failing on every attempt must render as its own picker
// row: round 1 only ever wrote it to stderr before the interactive picker
// replaced the whole screen with its alt-screen frame, so the notice was
// never actually seen — indistinguishable, at the visible surface, from "no
// update available".
func TestCachedProfessorUpdateFailureRowNamesAPersistentlyFailingChecker(t *testing.T) {
	runtime := releaseRuntimeWithSourceRepo(t)
	cachePath := professorUpdateCachePath(runtime)
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), cachePath, runtime.Version, failing.URL, failing.Client(),
	); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}

	row, ok := cachedProfessorUpdateFailureRow(runtime, false)
	if !ok || row.Kind != compose.ProfessorUpdateFailed {
		t.Fatalf("cachedProfessorUpdateFailureRow() = %+v, %t, want a ProfessorUpdateFailed row", row, ok)
	}
	if !strings.Contains(row.Name, "network") {
		t.Fatalf("row.Name = %q, want it to name the failure class", row.Name)
	}
}

// A real update found afterward makes the earlier failure history moot — the
// same rule professorUpdateCheckNotice already applies to its stderr line.
func TestCachedProfessorUpdateFailureRowStaysSilentWhenAnUpdateIsFound(t *testing.T) {
	runtime := releaseRuntimeWithSourceRepo(t)
	cachePath := professorUpdateCachePath(runtime)
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), cachePath, runtime.Version, failing.URL, failing.Client(),
	); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}

	if _, ok := cachedProfessorUpdateFailureRow(runtime, true); ok {
		t.Fatal("cachedProfessorUpdateFailureRow(hasUpdate=true) reported a row, want none")
	}
}

func TestCachedProfessorUpdateFailureRowStaysSilentWithNoFailure(t *testing.T) {
	runtime := releaseRuntimeWithSourceRepo(t)
	if _, ok := cachedProfessorUpdateFailureRow(runtime, false); ok {
		t.Fatal("cachedProfessorUpdateFailureRow() reported a row with no failure marker, want none")
	}
}
