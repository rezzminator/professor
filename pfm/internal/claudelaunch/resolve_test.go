package claudelaunch

import (
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestResolveSources(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, []string{home + "/.claude/projects/a", home + "/.cc/2/projects/b"})
	defaultWindowFound := false
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == "autoCompactWindow" {
			defaultWindowFound = true
			if row.Value != "100000" || row.Won != "default" {
				t.Errorf("default auto compact window=%#v", row)
			}
		}
	}
	if !defaultWindowFound {
		t.Fatal("default auto compact window row missing")
	}
	machine.Claude.Theme = "dark"
	machine.Claude.AutoCompactWindow = 250000
	machine.Sources["claude.theme"] = pfmconfig.SourceFile
	machine.Sources["claude.autoCompactWindow"] = pfmconfig.SourceFile
	machine.MCPServers["harvester"] = pfmconfig.MCPServer{Enabled: true}
	machine.Sources["harvester.enabled"] = pfmconfig.SourceFile
	prefs := machine.Claude
	prefs.Theme = "light"
	prefs.AutoCompactWindow = 50000
	machine.Accounts[1].Claude = &prefs
	machine.Sources["accounts[1].claude.theme"] = pfmconfig.SourceFile
	machine.Sources["accounts[1].claude.autoCompactWindow"] = pfmconfig.SourceFile
	get := func(account int, name string) Resolved {
		for _, row := range Resolve(machine, account) {
			if row.Knob.Name == name {
				return row
			}
		}
		t.Fatalf("missing row %s", name)
		return Resolved{}
	}
	if row := get(0, "theme"); row.Value != "dark" || row.Won != "config" {
		t.Errorf("machine theme=%#v", row)
	}
	if row := get(2, "theme"); row.Value != "light" || row.Won != "account" {
		t.Errorf("account theme=%#v", row)
	}
	if row := get(2, "configDir"); row.Won != "account" || row.Value != machine.Accounts[1].ConfigDir {
		t.Errorf("config dir=%#v", row)
	}
	if row := get(0, "outputStyle"); row.Won != "constant" || row.Value != "default" {
		t.Errorf("constant=%#v", row)
	}
	if row := get(0, "cache1h"); row.Won != "default" || row.Value != "true" {
		t.Errorf("default=%#v", row)
	}
	if row := get(0, "autoCompactWindow"); row.Won != "config" || row.Value != "250000" {
		t.Errorf("machine auto compact window=%#v", row)
	}
	if row := get(2, "autoCompactWindow"); row.Won != "account" || row.Value != "50000" {
		t.Errorf("account auto compact window=%#v", row)
	}
	if row := get(0, "functionHooks"); row.Won != "constant" || row.Value != "1" {
		t.Errorf("function hooks=%#v", row)
	}
	if row := get(0, "mcp"); row.Won != "config" || row.Value != "harvester" {
		t.Errorf("mcp=%#v", row)
	}
}

func TestResolveSameValueAccountSource(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	content := `{"version":2,"accounts":[{"id":2,"configDir":"` + home + `/.cc/2","claude":{"permissionMode":"bypass"}}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	machine, err := pfmconfig.Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range Resolve(machine, 2) {
		if row.Knob.Name == "permissionMode" {
			if row.Value != "bypass" || row.Won != "account" {
				t.Errorf("same-value override=%#v", row)
			}
			return
		}
	}
	t.Fatal("permissionMode row missing")
}

// The launch's one professor stdio server carries no loopback port, so a
// configured mcp.http.port never makes the mcp knob a config win.
func TestResolveMCPIgnoresHTTPPort(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, []string{home + "/.claude/projects/a"})
	machine.MCP.HTTP.Port = 19000
	machine.Sources["mcp.http.port"] = pfmconfig.SourceFile
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == "mcp" && row.Won == "config" {
			t.Errorf("mcp=%#v won by mcp.http.port alone", row)
		}
	}
}
