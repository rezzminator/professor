package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestHostSettingsProbe(t *testing.T) {
	home := t.TempDir()
	if ManagedRoot(home) != managedRootForHome(home) {
		t.Fatal("managed root differs")
	}
	path := filepath.Join(home, "physical-settings.json")
	raw, owned := accountSettingsFixture(home)
	writeFixture(t, path, string(raw))
	link := filepath.Join(home, "settings.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	ledger := settingsHookOwnershipPath(ManagedRoot(home))
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{physicalSettingsPath(link): owned})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, ledger, string(encoded))
	names, err := PFMSettingsLeftovers(home, link)
	if err != nil || !reflect.DeepEqual(names, []string{"hooks", "statusLine", "subagentStatusLine"}) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	// A ledger-owned command that does not match a pfm template still belongs to pfm.
	writeFixture(
		t,
		path,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"`+home+`/private-ledger-hook"}]}]}}`,
	)
	names, err = PFMSettingsLeftovers(home, link)
	if err != nil || !reflect.DeepEqual(names, []string{"hooks"}) {
		t.Fatalf("ledger names=%v err=%v", names, err)
	}
	writeFixture(t, ledger, "{")
	_, err = PFMSettingsLeftovers(home, link)
	assertProbePath(t, err, ledger)
}

func TestHostMCPProbes(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "registry.json")
	ledger := filepath.Join(ManagedRoot(home), mcpOwnershipName)
	registration := map[string]any{
		"type":    "stdio",
		"command": filepath.Join(home, ".local", "bin", "pfm"),
		"args":    mcpStdioArgs,
	}
	raw, err := json.Marshal(
		map[string]any{
			"mcpServers": map[string]any{
				"owned":     map[string]any{"command": "invented"},
				"professor": registration,
				"foreign":   map[string]any{"command": "other"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(raw))
	encoded, err := json.Marshal(
		mcpOwnership{
			Registrations: map[string]map[string]any{physicalSettingsPath(path): {"owned": map[string]any{}}},
			Clients:       []string{"owned"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, ledger, string(encoded))
	got, err := PFMMCPLeftovers(home, config.DefaultMCPPort, path)
	want := []string{"mcpServers.owned", "mcpServers.professor"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("names=%v err=%v", got, err)
	}
	writeFixture(t, filepath.Join(home, ".mcp.json"), string(raw))
	got, clients, err := PFMHomeMCPLeftovers(home, config.DefaultMCPPort)
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(clients, []string{"owned"}) {
		t.Fatalf("home names=%v clients=%v err=%v", got, clients, err)
	}
	writeFixture(t, ledger, "{")
	_, err = PFMMCPLeftovers(home, config.DefaultMCPPort, path)
	assertProbePath(t, err, ledger)
	_, _, err = PFMHomeMCPLeftovers(home, config.DefaultMCPPort)
	assertProbePath(t, err, ledger)
}

func assertProbePath(t *testing.T, err error, path string) {
	t.Helper()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != path {
		t.Fatalf("error=%v, want path %s", err, path)
	}
}

func TestRetiredMemoryHelperProbe(t *testing.T) {
	for _, test := range []struct{ old, new string }{{"cc-memory-wire.sh", "memory-wire.sh"}, {"cc-memory-consolidate.sh", "memory-consolidate.sh"}} {
		t.Run(test.old, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.old)
			content := oldMemoryHelperFixture(t, test.new, "invented-vault")
			writeFixture(t, path, string(content))
			newName, matched, err := RetiredMemoryHelper(path)
			if err != nil || !matched || newName != test.new {
				t.Fatalf("new=%s matched=%v err=%v", newName, matched, err)
			}
			writeFixture(t, path, string(append(content, []byte("# edited\n")...)))
			if _, matched, err = RetiredMemoryHelper(path); err != nil || matched {
				t.Fatalf("edited matched=%v err=%v", matched, err)
			}
			writeFixture(t, path, string(bytes.Replace(content, []byte("invented-vault"), []byte("$HOME"), 1)))
			if _, matched, err = RetiredMemoryHelper(path); err != nil || matched {
				t.Fatalf("unsafe matched=%v err=%v", matched, err)
			}
		})
	}
	if _, matched, err := RetiredMemoryHelper(filepath.Join(t.TempDir(), "unknown.sh")); matched || err != nil {
		t.Fatalf("unknown=%v %v", matched, err)
	}
}

func TestIsStagedShimLine(t *testing.T) {
	for _, test := range []struct {
		line string
		want bool
	}{
		{`source "$HOME/.local/share/pfm/install/shim/pfm.zsh"`, true},
		{`source "$HOME/.cc/cc-fleet.zsh"`, true},
		{`source "$HOME/clone/pfm/internal/installer/assets/shim/pfm.zsh"`, false},
		{`source '$HOME/clone/pfm/internal/installer/assets/shim/pfm.zsh' # keep`, false},
		{`# source "$HOME/old/pfm.zsh"`, false},
		{"echo unrelated", false},
	} {
		t.Run(test.line, func(t *testing.T) {
			if got := IsStagedShimLine(test.line); got != test.want {
				t.Fatalf("got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestHostProbesAbsenceAndUnreadable(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "missing")
	for _, probe := range []func() error{
		func() error { _, err := PFMSettingsLeftovers(home, path); return err },
		func() error { _, err := PFMMCPLeftovers(home, config.DefaultMCPPort, path); return err },
		func() error { _, _, err := PFMHomeMCPLeftovers(home, config.DefaultMCPPort); return err },
		func() error { _, _, err := RetiredMemoryHelper(filepath.Join(home, "cc-memory-wire.sh")); return err },
	} {
		if err := probe(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"settings.json", "registry.json", ".mcp.json", "cc-memory-wire.sh"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, err := PFMSettingsLeftovers(home, filepath.Join(home, "settings.json"))
	assertProbePath(t, err, filepath.Join(home, "settings.json"))
	_, err = PFMMCPLeftovers(home, config.DefaultMCPPort, filepath.Join(home, "registry.json"))
	assertProbePath(t, err, filepath.Join(home, "registry.json"))
	_, _, err = PFMHomeMCPLeftovers(home, config.DefaultMCPPort)
	assertProbePath(t, err, filepath.Join(home, ".mcp.json"))
	_, _, err = RetiredMemoryHelper(filepath.Join(home, "cc-memory-wire.sh"))
	assertProbePath(t, err, filepath.Join(home, "cc-memory-wire.sh"))
}
