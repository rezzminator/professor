package picker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/ui"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

func releaseRuntime(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	return pfmconfig.Runtime{
		Version: "v1.0.0",
		Paths:   paths.Values{CacheDB: filepath.Join(t.TempDir(), "pfm-cache.db")},
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
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
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
	if notice != "pfm ls: could not read the Professor update cache: decode update cache: invalid character 'n' looking for beginning of object key string" {
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
	if got, want := regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ][\d:.]+Z?|\d+(ns|µs|ms|s|m|h)\b`).
		ReplaceAllString(notice, "{t}"),
		"pfm ls: Professor update check failing since {t} (network): latest Professor release returned 503 Service Unavailable; its source repository cannot be used: read source repository marker .local/share/pfm/install/source-repo: no source repository recorded: open .local/share/pfm/install/source-repo: no such file or directory — run pfm install --yes from the clone"; got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestProfessorUpdateCheckNoticeStaysSilentWhenAnUpdateIsAlreadyRendered:
// cachedProfessorUpdateRow already surfaces a found update as its own picker
// row — a second stderr line would only repeat it.
func TestProfessorUpdateCheckNoticeStaysSilentWhenAnUpdateIsAlreadyRendered(t *testing.T) {
	runtime := releaseRuntimeWithSourceRepo(t)
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

// writeFoundUpdateCache records a successful check that found v1.2.0.
func writeFoundUpdateCache(t *testing.T, runtime pfmconfig.Runtime) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v1.2.0")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), professorUpdateCachePath(runtime), runtime.Version, server.URL, server.Client(),
	); err != nil {
		t.Fatal(err)
	}
}

// writeFailingUpdateCache records a detached checker failing with a 503.
func writeFailingUpdateCache(t *testing.T, runtime pfmconfig.Runtime) {
	t.Helper()
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := updatecheck.CheckForUpdate(
		context.Background(), professorUpdateCachePath(runtime), runtime.Version, failing.URL, failing.Client(),
	); err == nil {
		t.Fatal("CheckForUpdate() against a failing server returned nil error")
	}
}

// releaseRuntimeWithoutSourceRepo is a release runtime whose home holds no
// source-repo marker: the update and failure rows cannot render there.
func releaseRuntimeWithoutSourceRepo(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	runtime := releaseRuntime(t)
	runtime.Paths.Home = t.TempDir()
	return runtime
}

// A found update whose source-repo marker cannot be read renders no row (the
// update chat needs a usable clone); staying silent too would read as "no
// update available".
func TestProfessorUpdateCheckNoticeNamesAFoundUpdateWithAnUnusableSourceRepo(t *testing.T) {
	runtime := releaseRuntimeWithoutSourceRepo(t)
	writeFoundUpdateCache(t, runtime)
	if row, ok := cachedProfessorUpdateRow(runtime); ok {
		t.Fatalf("cachedProfessorUpdateRow() = %+v, want no row without a usable source repo", row)
	}
	_, markerErr := paths.ReadSourceRepoMarker(runtime.Paths.Home)
	if markerErr == nil {
		t.Fatal("fixture home unexpectedly holds a readable source-repo marker")
	}
	want := "pfm ls: Professor v1.2.0 is available but its source repository cannot be used: " +
		markerErr.Error() + " — run pfm install --yes from the clone"
	if notice := professorUpdateCheckNotice(runtime); notice != want {
		t.Fatalf("notice = %q, want %q", notice, want)
	}
}

func TestProfessorUpdateCheckNoticeNamesAFailingCheckWithAnUnusableSourceRepo(t *testing.T) {
	runtime := releaseRuntimeWithoutSourceRepo(t)
	writeFailingUpdateCache(t, runtime)
	if row, ok := cachedProfessorUpdateFailureRow(runtime, false); ok {
		t.Fatalf("cachedProfessorUpdateFailureRow() = %+v, want no row without a usable source repo", row)
	}
	_, markerErr := paths.ReadSourceRepoMarker(runtime.Paths.Home)
	if markerErr == nil {
		t.Fatal("fixture home unexpectedly holds a readable source-repo marker")
	}
	notice := professorUpdateCheckNotice(runtime)
	if got, want := regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ][\d:.]+Z?|\d+(ns|µs|ms|s|m|h)\b`).
		ReplaceAllString(strings.ReplaceAll(notice, markerErr.Error(), "{marker error}"), "{t}"),
		"pfm ls: Professor update check failing since {t} (network): latest Professor release returned 503 Service Unavailable; its source repository cannot be used: {marker error} — run pfm install --yes from the clone"; got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestOpenProfessorUpdateOpensTheChatWithThe1hCache pins that the update chat
// always runs at 1h, whether or not the picker's own cache toggle is on, for
// every engine.
func TestOpenProfessorUpdateOpensTheChatWithThe1hCache(t *testing.T) {
	type openCall struct {
		row     compose.Row
		primary int
		cache1H bool
		prompt  string
	}
	updateRow := compose.Row{
		Kind: compose.ProfessorUpdate,
		ID:   "pfm-update-v1.2.3",
		Name: "v1.2.3",
		CWD:  "/src/professor",
	}
	wantPrompt := professorUpdatePrompt(updateRow)
	if !strings.HasPrefix(wantPrompt, "Professor v1.2.3 is available.") {
		t.Fatalf("prompt = %q, want it to open with %q", wantPrompt, "Professor v1.2.3 is available.")
	}
	tests := []struct {
		name     string
		engine   pfmengine.ID
		toggle   bool
		wantKind compose.Kind
	}{
		{name: "claude toggle off", engine: pfmengine.Claude, toggle: false, wantKind: compose.NewClaude},
		{name: "claude toggle on", engine: pfmengine.Claude, toggle: true, wantKind: compose.NewClaude},
		{name: "codex toggle off", engine: pfmengine.Codex, toggle: false, wantKind: compose.NewCodex},
		{name: "opencode toggle off", engine: pfmengine.OpenCode, toggle: false, wantKind: compose.NewOpenCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []openCall
			original := openChatRow
			t.Cleanup(func() { openChatRow = original })
			openChatRow = func(
				_ context.Context, row compose.Row, primary int, cache1H bool, prompt string,
				_, _ io.Writer, _ *pfmconfig.Runtime,
			) int {
				calls = append(calls, openCall{row: row, primary: primary, cache1H: cache1H, prompt: prompt})
				return 7
			}
			outcome := ui.Outcome{
				Kind: ui.OutcomeProfessorUpdate, Row: updateRow, Engine: test.engine,
				PrimaryAccount: 2, Cache1H: test.toggle,
			}
			var stderr bytes.Buffer
			code := openProfessorUpdate(context.Background(), outcome, io.Discard, &stderr, pfmconfig.Runtime{})
			if code != 7 || stderr.Len() != 0 {
				t.Fatalf("code = %d, stderr = %q, want 7 and empty", code, stderr.String())
			}
			if len(calls) != 1 {
				t.Fatalf("OpenRow calls = %d, want 1", len(calls))
			}
			got := calls[0]
			if !got.cache1H {
				t.Errorf("cache1H = false, want true (picker toggle %v)", test.toggle)
			}
			if got.row.Name != "Professor update v1.2.3" || got.row.Kind != test.wantKind || got.row.Account != 2 ||
				got.primary != 2 {
				t.Errorf("row = {Name %q Kind %v Account %d} primary %d, want {Professor update v1.2.3 %v 2} 2",
					got.row.Name, got.row.Kind, got.row.Account, got.primary, test.wantKind)
			}
			if got.prompt != wantPrompt {
				t.Errorf("prompt = %q, want %q", got.prompt, wantPrompt)
			}
		})
	}
}
