package claudelaunch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestResolveSources(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, []string{home + "/.claude/projects/a", home + "/.cc/2/projects/b"})
	defaultWindowFound, defaultHelperFound := false, false
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == "autoCompactWindow" {
			defaultWindowFound = true
			if row.Value != "100000" || row.Won != "default" {
				t.Errorf("default auto compact window=%#v", row)
			}
		}
		if row.Knob.Name == "helperModel" {
			defaultHelperFound = true
			if row.Value != "claude-haiku-5-5" || row.Won != "default" {
				t.Errorf("default helper model=%#v", row)
			}
		}
	}
	if !defaultWindowFound || !defaultHelperFound {
		t.Fatalf("default rows missing: auto compact window=%t helper model=%t", defaultWindowFound, defaultHelperFound)
	}
	machine.Claude.Theme = "dark"
	machine.Claude.AutoCompactWindow = 250000
	machine.Claude.HelperModel = "claude-haiku-4-5"
	machine.Sources["claude.theme"] = pfmconfig.SourceFile
	machine.Sources["claude.autoCompactWindow"] = pfmconfig.SourceFile
	machine.Sources["claude.helperModel"] = pfmconfig.SourceFile
	prefs := machine.Claude
	prefs.Theme = "light"
	prefs.AutoCompactWindow = 50000
	prefs.HelperModel = "claude-sonnet-5-5"
	machine.Accounts[1].Claude = &prefs
	machine.Sources["accounts[1].claude.theme"] = pfmconfig.SourceFile
	machine.Sources["accounts[1].claude.autoCompactWindow"] = pfmconfig.SourceFile
	machine.Sources["accounts[1].claude.helperModel"] = pfmconfig.SourceFile
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
	if row := get(1, "configDir"); row.Won != "account" || row.Value != machine.Accounts[0].ConfigDir {
		t.Errorf("account one config dir=%#v", row)
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
	if row := get(0, "helperModel"); row.Won != "config" || row.Value != "claude-haiku-4-5" {
		t.Errorf("machine helper model=%#v", row)
	}
	if row := get(2, "helperModel"); row.Won != "account" || row.Value != "claude-sonnet-5-5" {
		t.Errorf("account helper model=%#v", row)
	}
	if row := get(0, "functionHooks"); row.Won != "constant" || row.Value != "1" {
		t.Errorf("function hooks=%#v", row)
	}
	if row := get(0, "mcp"); row.Won != "default" || row.Value != unsetWord {
		t.Errorf("mcp=%#v, want default unset", row)
	}
}

func TestResolveSameValueAccountSource(t *testing.T) {
	const unread = " (account overrides unread: decode PATH: invalid character 'g' looking for beginning of value)"
	for _, test := range []struct{ name, mode, rewrite, want string }{
		{name: "override keys read", mode: "bypass", want: "account"},
		{
			// The document changed unreadably after the load: an override equal to its
			// parent cannot be told from inheritance, and the row says its keys went unread.
			name: "same value, override keys unread", mode: "bypass", rewrite: "garbage", want: "default" + unread,
		},
		{
			// A value differing from its parent is the account's even with its keys unread.
			name: "differing value, override keys unread", mode: "prompted", rewrite: "garbage", want: "account" + unread,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(t.TempDir(), "pfm.config.json")
			content := `{"version":2,"accounts":[{"id":2,"configDir":"` + home + `/.cc/2","claude":{"permissionMode":"` +
				test.mode + `"}}]}`
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			machine, err := pfmconfig.Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.rewrite != "" {
				if err := os.WriteFile(path, []byte(test.rewrite), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := strings.ReplaceAll(test.want, "PATH", path)
			for _, row := range Resolve(machine, 2) {
				if row.Knob.Name == "permissionMode" {
					if row.Value != test.mode || row.Won != want {
						t.Errorf("permissionMode row=%#v, want value %q won %q", row, test.mode, want)
					}
					return
				}
			}
			t.Fatal("permissionMode row missing")
		})
	}
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
