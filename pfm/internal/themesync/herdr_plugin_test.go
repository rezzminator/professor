package themesync

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func linkCalls(h *harness) int {
	count := 0
	for _, call := range h.runner.Calls() {
		if len(call.Argv) == 4 && call.Argv[1] == "plugin" && call.Argv[2] == "link" {
			count++
		}
	}
	return count
}

func TestHerdrPluginManifestRender(t *testing.T) {
	got := herdrPluginRender("/opt/bin/pfm")
	for _, event := range []string{"pane.created", "tab.created"} {
		if !strings.Contains(got, "on = "+strconv.Quote(event)) {
			t.Errorf("manifest lacks topology event %s", event)
		}
	}
	for _, want := range []string{
		"id = \"professor.indicators\"\n", "min_herdr_version = \"0.9.5\"\n", "platforms = [\"macos\", \"linux\"]\n",
		"\n[[startup]]\ncommand = [\"/opt/bin/pfm\", \"internal\", \"herdr-indicators\"]\n",
		"\n[[startup]]\ncommand = [\"/opt/bin/pfm\", \"internal\", \"herdr-places\"]\n",
		"\n[[events]]\non = \"pane.agent_status_changed\"\ncommand = [\"/opt/bin/pfm\", \"internal\", \"herdr-indicators\"]\n",
		"\n[[events]]\non = \"workspace.created\"\n",
		"\n[[events]]\non = \"pane.focused\"\n",
		"\n[[events]]\non = \"tab.focused\"\n",
		"\n[[events]]\non = \"workspace.focused\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "[[startup]]") != 2 || strings.Count(got, "[[events]]") != 13 {
		t.Errorf("manifest wants two startups and 13 events:\n%s", got)
	}
}

func TestHerdrApplyWritesTheManifestAndLinksWhenTheRegistryLacksIt(t *testing.T) {
	h := newHarness(t)
	h.write(herdrRegistryRel, "[]")
	// the fake herdr links nothing into the registry, so the read-back that
	// closes the run still sees the plugin unlinked
	wantVerdict(t, h.on(), "herdr", VerdictDrift, "is not linked in Herdr")
	if got := h.read(herdrManifestRel); got != herdrPluginRender("/opt/bin/pfm") {
		t.Fatalf("manifest on disk =\n%s", got)
	}
	var link []string
	for _, call := range h.runner.Calls() {
		if len(call.Argv) >= 3 && call.Argv[1] == "plugin" {
			link = call.Argv
		}
	}
	if want := []string{
		herdrBinary,
		"plugin",
		"link",
		h.path(".config/herdr/pfm-indicators"),
	}; strings.Join(
		link,
		" ",
	) != strings.Join(
		want,
		" ",
	) {
		t.Fatalf("link argv = %v, want %v", link, want)
	}
	h.write(herdrRegistryRel, `[{"plugin_id":"professor.indicators"}]`)
	wantVerdict(t, h.status(), "herdr", VerdictInSync, "")
}

func TestHerdrApplyLinksOnceWhenTheRegistryAlreadyHasThePlugin(t *testing.T) {
	h := newHarness(t)
	h.on()
	if got := linkCalls(h); got != 1 {
		t.Fatalf(
			"first apply wrote the manifest and linked %d times, want 1",
			got,
		)
	}
	h.sync(true)
	if got := linkCalls(h); got != 1 {
		t.Fatalf(
			"a forced apply with the manifest current and the plugin linked linked again: %d link calls, want 1",
			got,
		)
	}
	wantVerdict(t, h.status(), "herdr", VerdictInSync, "")
}

func TestHerdrApplyLinkFailureIsAFailedRowNamingIt(t *testing.T) {
	h := newHarness(t)
	h.write(herdrRegistryRel, "[]")
	h.herdrLink = deps.RunResult{ExitCode: 1, Stderr: []byte("plugin_registry_save_failed")}
	h.rearm()
	report := h.on()
	wantVerdict(t, report, "herdr", VerdictError, "plugin link")
	wantVerdict(t, report, "herdr", VerdictError, "plugin_registry_save_failed")
	if report.OK() {
		t.Error("a failed link left the report OK")
	}
	if got := reloadCalls(h); got != 1 {
		t.Errorf("reload-config calls = %d, want the due reload run despite the failed link", got)
	}
}

func reloadCalls(h *harness) int {
	count := 0
	for _, call := range h.runner.Calls() {
		if len(call.Argv) == 3 && call.Argv[1] == "server" && call.Argv[2] == "reload-config" {
			count++
		}
	}
	return count
}

func TestHerdrOffClearsTheIndicatorsOfEveryRecordedSocket(t *testing.T) {
	h := newHarness(t)
	h.on()
	gone := filepath.Join(h.path(".local/state/pfm/herdr"), "indicators-00000000.socket")
	h.write(".local/state/pfm/herdr/indicators-00000000.socket", filepath.Join(t.TempDir(), "gone.sock"))
	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "")
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("off left the record of a gone server: stat %v", err)
	}
}

// The theme off's stop marker outlives it, so a painter that starts later
// stands down; the theme on retires it, on a first apply and on a forced apply
// that finds the plugin linked and its manifest current.
func TestHerdrOnRetiresTheThemeOffStopMarkers(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.write(".local/state/pfm/herdr/indicators-00000000.socket", filepath.Join(t.TempDir(), "gone.sock"))
	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "")
	markers := func() []string {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(h.path(".local/state/pfm/herdr"), "indicators-*.stop"))
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	if got := markers(); len(got) != 1 {
		t.Fatalf("theme off left stop markers %v, want its one marker", got)
	}
	h.on()
	if got := markers(); len(got) != 0 {
		t.Fatalf("theme on left the theme off's stop markers %v", got)
	}
	h.write(".local/state/pfm/herdr/indicators-11111111.stop", "1 2")
	h.sync(true)
	if got := markers(); len(got) != 0 {
		t.Fatalf("a forced apply with the plugin linked left the stop markers %v", got)
	}
}

func TestHerdrOffThatCannotClearTheIndicatorsIsAFailedRow(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.write(".local/state/pfm/herdr/indicators-00000000.socket/blocker", "a record that is a directory")
	wantVerdict(t, offReport(t, h), "herdr", VerdictError, "clear the herdr indicators")
}

func TestHerdrApplyLinkWithNoServerIsDeferredNotFailed(t *testing.T) {
	h := newHarness(t)
	h.write(herdrRegistryRel, "[]")
	h.herdrLink = deps.RunResult{ExitCode: 1, Stderr: []byte("herdr: connection refused")}
	h.rearm()
	report := h.on()
	wantVerdict(t, report, "herdr", VerdictDrift, "is not linked in Herdr")
	if got := linkCalls(h); got != 1 {
		t.Errorf("link calls = %d, want the one attempt", got)
	}
}

func TestHerdrReadReportsAManifestThatDiffers(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.write(herdrManifestRel, "id = \"professor.indicators\"\n")
	wantVerdict(t, h.status(), "herdr", VerdictDrift, "manifest differs")
	h.sync(false)
	if got := h.read(herdrManifestRel); got != herdrPluginRender("/opt/bin/pfm") {
		t.Fatalf("manifest not healed:\n%s", got)
	}
	if err := os.RemoveAll(h.path(".config/herdr/pfm-indicators")); err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, h.status(), "herdr", VerdictDrift, "manifest missing")
}

func TestHerdrOffUnlinksThePluginAndRemovesItsDirectory(t *testing.T) {
	h := newHarness(t)
	h.on()
	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "")
	var unlink []string
	for _, call := range h.runner.Calls() {
		if len(call.Argv) == 4 && call.Argv[2] == "unlink" {
			unlink = call.Argv
		}
	}
	if strings.Join(unlink, " ") != herdrBinary+" plugin unlink professor.indicators" {
		t.Fatalf("unlink argv = %v", unlink)
	}
	if h.exists(".config/herdr/pfm-indicators") {
		t.Fatal("off left the plugin directory")
	}
}

func TestHerdrOffUnlinkFailureIsAFailedRowAndDeferredWhenNoServer(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.herdrUnlink = deps.RunResult{ExitCode: 1, Stderr: []byte("no such plugin")}
	h.rearm()
	wantVerdict(t, offReport(t, h), "herdr", VerdictError, "plugin unlink")
	h.herdrUnlink = deps.RunResult{ExitCode: 1, Stderr: []byte("connection refused")}
	h.rearm()
	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "herdr plugin unlink professor.indicators")
	if h.exists(".config/herdr/pfm-indicators") {
		t.Fatal("a deferred unlink must still remove the plugin directory")
	}
}
