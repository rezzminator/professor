package action

import (
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

func TestCodexLaunchesUseTheSelectedRosterHome(t *testing.T) {
	machine := pfmconfig.Config{
		Version: pfmconfig.Version,
		CodexAccounts: []pfmconfig.CodexAccount{
			{ID: 7, Home: "/codex/one"},
			{ID: 9, Home: "/codex/two", Prefs: &pfmconfig.CodexPrefs{Yolo: false, Binary: "/opt/codex-safe"}},
		},
		Codex:   pfmconfig.CodexPrefs{Yolo: true, Binary: "codex"},
		Sources: map[string]pfmconfig.Source{},
	}

	headless, err := HeadlessRun(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work/project",
		PrimaryAccount: 9, Config: machine,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	for _, want := range []string{"CODEX_HOME='/codex/two'", "'/opt/codex-safe'", "--sandbox workspace-write"} {
		if !strings.Contains(headless.Run, want) {
			t.Fatalf("headless run %q lacks %q", headless.Run, want)
		}
	}

	picker, err := Synthesize(Request{
		Row:            compose.Row{Kind: compose.NewCodex, CWD: "/work/project"},
		PrimaryAccount: 7,
		Config:         machine,
	})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if !strings.Contains(picker.Line, "CODEX_HOME='/codex/one' cx") {
		t.Fatalf("picker line = %q", picker.Line)
	}

	_, err = HeadlessRun(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work/project",
		PrimaryAccount: 8, Config: machine,
	})
	if err == nil || !strings.Contains(err.Error(), "requested Codex account 8") {
		t.Fatalf("off-roster error = %v", err)
	}
}
