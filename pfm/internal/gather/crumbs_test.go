package gather

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
