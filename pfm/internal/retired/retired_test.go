package retired

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestShippedListsLoad: the clone's own file parses and validates.
func TestShippedListsLoad(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLists(repo); err != nil {
		t.Fatalf("ReadLists(%s) = %v; want the shipped %s to load", repo, err, FileRel)
	}
}

func TestParse(t *testing.T) {
	full := `{
  "names": [{"name": "/old", "kind": "command", "successor": "/new", "match": "family", "route": true},
            {"name": "x agent", "kind": "agent", "match": "exact", "probe": ".claude/agents/x.md"}],
  "storeEntries": ["stats-cache.json"],
  "stateDirs": [{"name": "call-cap", "what": "retired tallies"}],
  "hookCommands": [{"name": "bb", "subcommand": "chat bb"}],
  "hookBinaries": ["cc-fleet"],
  "hookShimHints": [{"name": "bb", "hint": "bb-hook.sh"}],
  "claudePlugins": {"new-effort": ["old-effort@professor"]},
  "globalAgents": ["frr"],
  "units": ["cc-name-sync.timer"],
  "configFiles": [{"path": "bin/cx-recover.sh.pre-professor-*", "reason": "retired recovery artifact"}],
  "commands": {"homeBin": ["cc-ls"], "accountBin": ["cc-launch.sh"]}
}`
	want := Lists{
		Names: []Name{
			{Name: "/old", Kind: KindCommand, Successor: "/new", Match: MatchFamily, Route: true},
			{Name: "x agent", Kind: KindAgent, Match: MatchExact, Probe: ".claude/agents/x.md"},
		},
		StoreEntries:  []string{"stats-cache.json"},
		StateDirs:     []StateDir{{Name: "call-cap", What: "retired tallies"}},
		HookCommands:  []HookCommand{{Name: "bb", Subcommand: "chat bb"}},
		HookBinaries:  []string{"cc-fleet"},
		HookShimHints: []HookShimHint{{Name: "bb", Hint: "bb-hook.sh"}},
		ClaudePlugins: map[string][]string{"new-effort": {"old-effort@professor"}},
		GlobalAgents:  []string{"frr"},
		Units:         []string{"cc-name-sync.timer"},
		ConfigFiles:   []ConfigFile{{Path: "bin/cx-recover.sh.pre-professor-*", Reason: "retired recovery artifact"}},
		Commands:      Commands{HomeBin: []string{"cc-ls"}, AccountBin: []string{"cc-launch.sh"}},
	}
	got, err := ParseLists([]byte(full))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLists(full) = %+v, %v; want %+v, nil", got, err, want)
	}

	for _, test := range []struct{ name, raw, want string }{
		{"unknown key", `{"name": []}`, `decode: json: unknown field "name"`},
		{"trailing data", `{} {}`, "decode: trailing data after the object"},
		{
			"unknown kind", `{"names": [{"name": "/x", "kind": "verb", "match": "exact"}]}`,
			`names[0] "/x": unknown kind "verb"`,
		},
		{
			"unknown match", `{"names": [{"name": "/x", "kind": "command", "match": "fuzzy"}]}`,
			`names[0] "/x": unknown match "fuzzy"`,
		},
		{"empty name", `{"names": [{"name": "", "kind": "command", "match": "exact"}]}`, "names[0]: empty name"},
		{
			"probe outside the root", `{"names": [{"name": "x", "kind": "agent", "match": "exact", "probe": "../x.md"}]}`,
			`names[0] "x": probe "../x.md" is not a root-relative path`,
		},
		{"store entry with a slash", `{"storeEntries": ["a/b"]}`, `storeEntries[0]: "a/b" is not one file name`},
		{
			"state dir without what", `{"stateDirs": [{"name": "cutgate", "what": ""}]}`,
			`stateDirs[0]: want a directory name and what it held, got "cutgate", ""`,
		},
		{
			"hook without a subcommand", `{"hookCommands": [{"name": "bb", "subcommand": " "}]}`,
			`hookCommands[0]: want a name and a subcommand, got "bb", " "`,
		},
		{"hook binary path", `{"hookBinaries": ["/x/cc-fleet"]}`, `hookBinaries[0]: "/x/cc-fleet" is not one file name`},
		{
			"plugin id without a marketplace", `{"claudePlugins": {"new-effort": ["old-effort"]}}`,
			`claudePlugins["new-effort"][0]: want a name@marketplace id, got "old-effort"`,
		},
		{"agent with a path", `{"globalAgents": ["../frr"]}`, `globalAgents[0]: "../frr" is not one agent name`},
		{"unit with a path", `{"units": ["x/y.timer"]}`, `units[0]: "x/y.timer" is not one unit name`},
		{
			"config file outside the dir", `{"configFiles": [{"path": "../bin/x", "reason": "r"}]}`,
			`configFiles[0]: want a config-relative path or glob and a reason, got "../bin/x", "r"`,
		},
		{
			"config file bad glob", `{"configFiles": [{"path": "bin/[", "reason": "r"}]}`,
			`configFiles[0]: want a config-relative path or glob and a reason, got "bin/[", "r"`,
		},
		{
			"command with a path", `{"commands": {"homeBin": ["bin/cc-ls"], "accountBin": []}}`,
			`commands.homeBin[0]: "bin/cc-ls" is not one file name`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseLists([]byte(test.raw))
			if err == nil || err.Error() != test.want {
				t.Fatalf("ParseLists(%s) error = %v; want %q", test.raw, err, test.want)
			}
		})
	}
}

// TestLoadNamesTheFile: a clone without the file, or with a broken one, is an
// error naming the file, never empty lists; LoadListsIfPresent reads only the
// absent file as empty.
func TestLoadNamesTheFile(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, ".professor", filepath.FromSlash(FileRel))
	if _, err := LoadLists(home); !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), file) {
		t.Fatalf("Load without the file = %v; want not-exist naming %s", err, file)
	}
	if lists, err := LoadListsIfPresent(home); err != nil || !reflect.DeepEqual(lists, Lists{}) {
		t.Fatalf("LoadListsIfPresent without the file = %+v, %v; want empty lists, nil", lists, err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"units": [""]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "retired lists " + file + `: units[0]: "" is not one unit name`
	if _, err := LoadLists(home); err == nil || err.Error() != want {
		t.Fatalf("LoadLists(broken) = %v; want %q", err, want)
	}
	if _, err := LoadListsIfPresent(home); err == nil || err.Error() != want {
		t.Fatalf("LoadListsIfPresent(broken) = %v; want %q", err, want)
	}
	if err := os.WriteFile(file, []byte(`{"units": ["pfm-reminder.timer"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lists, err := LoadLists(home)
	if err != nil || !reflect.DeepEqual(lists.Units, []string{"pfm-reminder.timer"}) {
		t.Fatalf("Load = %+v, %v; want units [pfm-reminder.timer]", lists, err)
	}
}
