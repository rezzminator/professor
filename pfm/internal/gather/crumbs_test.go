package gather

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseCrumbNameStrictGrammar(t *testing.T) {
	tests := []struct {
		name   string
		socket string
		pane   string
		ok     bool
	}{
		{name: "cc-123-456-789", socket: "cc-123-456-789", ok: true},
		{name: "cx-123-456-789.%12", socket: "cx-123-456-789", pane: "%12", ok: true},
		{name: "cc-new-agent_name", socket: "cc-new-agent_name", ok: true},
		{name: "cc-new-agent-name.%9", socket: "cc-new-agent-name", pane: "%9", ok: true},
		{name: ""},
		{name: "../cc-1-2-3"},
		{name: "cc-1-2-3.then-failed"},
		{name: "cc-1-2-3.%"},
		{name: "cc-1-2-3.%x"},
		{name: "cc-1-2-3.%1.extra"},
		{name: "cc-epoch-2-3"},
		{name: "cc-1-2-3\nsentinel"},
		{name: strings.Repeat("a", 1024)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			socket, pane, ok := ParseCrumbName(test.name)
			if socket != test.socket || pane != test.pane || ok != test.ok {
				t.Fatalf(
					"ParseCrumbName(%q) = %q, %q, %v; want %q, %q, %v",
					test.name,
					socket,
					pane,
					ok,
					test.socket,
					test.pane,
					test.ok,
				)
			}
		})
	}
}

func TestReadCrumbsAndSweepStalePane(t *testing.T) {
	root := t.TempDir()
	setGatherTestEnv(t, root, root)
	sidDir := filepath.Join(root, "sid")
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatalf("create sid dir: %v", err)
	}
	files := map[string]string{
		"cc-1-2-3":                 "/transcripts/socket.jsonl",
		"cc-1-2-3.%1":              "/transcripts/pane.jsonl",
		"cc-1-2-3.%99":             "/transcripts/stale.jsonl",
		"cc-1-2-3.then-failed":     "/transcripts/sentinel.jsonl",
		"cc-new-agent":             "",
		"cx-4-5-6.takeover-marker": "/transcripts/ignored.jsonl",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write crumb %q: %v", name, err)
		}
	}

	probe, err := ReadCrumbs(sidDir, []ProbePane{{
		Socket: "cc-1-2-3",
		PaneID: "%1",
	}})
	if err != nil {
		t.Fatalf("ReadCrumbs() error = %v", err)
	}
	want := []Crumb{
		{
			Filename:       "cc-1-2-3",
			Socket:         "cc-1-2-3",
			TranscriptPath: "/transcripts/socket.jsonl",
		},
		{
			Filename:       "cc-1-2-3.%1",
			Socket:         "cc-1-2-3",
			PaneID:         "%1",
			TranscriptPath: "/transcripts/pane.jsonl",
		},
	}
	if !reflect.DeepEqual(probe.Crumbs, want) {
		t.Fatalf("ReadCrumbs().Crumbs = %#v, want %#v", probe.Crumbs, want)
	}
	if !reflect.DeepEqual(probe.StaleSwept, []string{"cc-1-2-3.%99"}) {
		t.Fatalf("ReadCrumbs().StaleSwept = %q", probe.StaleSwept)
	}
	if _, err := os.Stat(filepath.Join(sidDir, "cc-1-2-3.%99")); !os.IsNotExist(err) {
		t.Fatalf("stale pane crumb still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sidDir, "cc-1-2-3.then-failed")); err != nil {
		t.Fatalf("sentinel was touched: %v", err)
	}
}

func TestReadCrumbsReadOnlyLeavesStalePane(t *testing.T) {
	sidDir := t.TempDir()
	stale := filepath.Join(sidDir, "cc-1-2-3.%99")
	if err := os.WriteFile(stale, []byte("/transcripts/stale.jsonl"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := ReadCrumbsReadOnly(sidDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.Crumbs) != 0 || len(probe.StaleSwept) != 0 {
		t.Fatalf("read-only crumb probe = %#v", probe)
	}
	if content, err := os.ReadFile(stale); err != nil ||
		string(content) != "/transcripts/stale.jsonl" {
		t.Fatalf("read-only probe mutated stale crumb: content=%q err=%v", content, err)
	}
}

// overlongSIDDir builds a directory whose path is exactly length characters,
// written by populate under a short name and then renamed long: an entry
// whose own path then passes the platform's PATH_MAX (unix.PathMax) is refused to every
// user, root included, while its shorter siblings stay readable.
func overlongSIDDir(t *testing.T, length int, populate func(dir string)) string {
	t.Helper()
	parent := t.TempDir()
	for remaining := length - 201 - len(parent); remaining > 0; remaining = length - 201 - len(parent) {
		size := min(200, remaining-1)
		if remaining-size-1 == 1 { // a 1-character remainder cannot be a component
			size--
		}
		parent = filepath.Join(parent, strings.Repeat("d", size))
	}
	short := filepath.Join(parent, "s")
	if err := os.MkdirAll(short, 0o700); err != nil {
		t.Fatal(err)
	}
	populate(short)
	dir := filepath.Join(parent, strings.Repeat("s", 200))
	if err := os.Rename(short, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestScanCrumbsCostsOnlyTheCrumbItCannotReadOrSweep: a crumb that cannot be
// read, or a stale one that cannot be removed, is named in Warnings while the
// scan goes on; a pane crumb on a socket the tmux probe could not read is
// never swept, its pane may live on unread.
func TestScanCrumbsCostsOnlyTheCrumbItCannotReadOrSweep(t *testing.T) {
	longSocket := "cc-1-2-" + strings.Repeat("3", 100)
	files := map[string]string{
		"cc-1-2-3.%1":      "/transcripts/live.jsonl",
		"cc-1-2-3.%99":     "/transcripts/stale.jsonl",
		longSocket:         "/transcripts/unreadable.jsonl",
		longSocket + ".%7": "/transcripts/unremovable.jsonl",
		"cc-4-5-6.%3":      "/transcripts/on-an-unread-socket.jsonl",
	}
	sidDir := overlongSIDDir(t, unix.PathMax-96, func(dir string) {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	unread, unremovable := filepath.Join(sidDir, longSocket), filepath.Join(sidDir, longSocket+".%7")

	probe, err := scanCrumbs(sidDir, []ProbePane{{Socket: "cc-1-2-3", PaneID: "%1"}}, true,
		map[string]string{"cc-4-5-6": "could not read this tmux server"})
	if err != nil {
		t.Fatalf("scanCrumbs() error = %v, want the scan to go on", err)
	}
	if len(probe.Crumbs) != 1 || probe.Crumbs[0].TranscriptPath != "/transcripts/live.jsonl" {
		t.Fatalf("crumbs = %#v, want the live pane's crumb", probe.Crumbs)
	}
	if !slices.Equal(probe.StaleSwept, []string{"cc-1-2-3.%99"}) {
		t.Fatalf("StaleSwept = %q, want only cc-1-2-3.%%99", probe.StaleSwept)
	}
	wantWarnings := []string{
		"crumb " + unread + " unreadable; its chat may lack its transcript this scan: open " + unread +
			": file name too long",
		"stale pane crumb " + unremovable + " could not be removed; it stays: remove " + unremovable +
			": file name too long",
	}
	if !slices.Equal(probe.Warnings, wantWarnings) || probe.Ignored != 1 {
		t.Fatalf("warnings = %q ignored=%d, want %q and 1 ignored", probe.Warnings, probe.Ignored, wantWarnings)
	}
	if _, err := os.Stat(filepath.Join(sidDir, "cc-4-5-6.%3")); err != nil {
		t.Fatalf("crumb on an unread socket was swept: %v", err)
	}
}
