package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const envKey = "SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL"

// TestEnsureClaudeEnv: claude.env lands in the primary seat's settings.json
// env through its link, pfm's keys alone recorded in the ownership ledger; a
// key claude.env drops is removed unless it was changed by hand since pfm set
// it; every key pfm never set keeps its value.
func TestEnsureClaudeEnv(t *testing.T) {
	type state struct {
		store     string // ~/.claude/settings.json; "" = absent
		linked    bool   // the seat's settings.json links to the store
		other     string // ~/.cc/9/settings.json, a former primary seat; "" = absent
		ledger    string // the ledger's raw text; "" = absent
		ledgerFor map[string]map[string]string
	}
	for _, test := range []struct {
		name          string
		apply         bool
		uninstall     bool
		env           map[string]string
		given         state
		wantErr       string
		lines         []string // {seat}, {store} and {other} expand
		absent        []string
		wantStore     string // the store's env as JSON; "" = no env key; "-" = byte-identical
		wantOther     string
		wantOwned     map[string]map[string]string // nil = no ledger file
		wantMode      fs.FileMode
		unreadPlugins bool // the marketplace plugin list fails to read
	}{
		{
			name: "set through the seat link keeping mode and unowned keys", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: `{"counter":9007199254740993,"env":{"OTHER":"x"}}`, linked: true},
			lines:     []string{"  change  claude.env: set " + envKey + " in {seat}"},
			wantStore: `{"OTHER":"x","` + envKey + `":"1"}`,
			wantOwned: map[string]map[string]string{"{store}": {envKey: "1"}},
			wantMode:  0o644,
		},
		{
			name: "dry run writes nothing",
			env:  map[string]string{envKey: "1"},
			given: state{
				store: `{"env":{"OTHER":"x"}}`, linked: true,
			},
			lines:     []string{"  change  claude.env: set " + envKey + " in {seat}"},
			wantStore: "-",
		},
		{
			name: "an owned key claude.env dropped is removed", apply: true,
			env: map[string]string{},
			given: state{
				store: `{"env":{"OTHER":"x","` + envKey + `":"1"}}`, linked: true,
				ledgerFor: map[string]map[string]string{"{store}": {envKey: "1"}},
			},
			lines:     []string{"  change  claude.env: remove " + envKey + " in {seat}"},
			wantStore: `{"OTHER":"x"}`,
		},
		{
			name: "the last owned key removed drops env", apply: true,
			env: map[string]string{},
			given: state{
				store: `{"env":{"` + envKey + `":"1"}}`, linked: true,
				ledgerFor: map[string]map[string]string{"{store}": {envKey: "1"}},
			},
			lines:     []string{"  change  claude.env: remove " + envKey + " in {seat}"},
			wantStore: "",
		},
		{
			name: "an owned key changed by hand is left and forgotten", apply: true,
			env: map[string]string{},
			given: state{
				store: `{"env":{"` + envKey + `":"0"}}`, linked: true,
				ledgerFor: map[string]map[string]string{"{store}": {envKey: "1"}},
			},
			lines: []string{
				"  warn    claude.env " + envKey + " in {seat} was changed by hand since pfm set it: left in place, no longer pfm's",
			},
			absent:    []string{"  change  "},
			wantStore: "-",
		},
		{
			name: "an unowned key with another value is taken over", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: `{"env":{"` + envKey + `":0}}`, linked: true},
			lines:     []string{"  change  claude.env: set " + envKey + " in {seat}"},
			wantStore: `{"` + envKey + `":"1"}`,
			wantOwned: map[string]map[string]string{"{store}": {envKey: "1"}},
			wantMode:  0o644,
		},
		{
			name: "an absent settings file is created 0600", apply: true,
			env:       map[string]string{envKey: "1"},
			lines:     []string{"  change  claude.env: set " + envKey + " in {seat}"},
			wantOwned: map[string]map[string]string{"{seatfile}": {envKey: "1"}},
		},
		{
			name: "uninstall removes owned keys and the ledger", apply: true, uninstall: true,
			env: map[string]string{envKey: "1"},
			given: state{
				store: `{"env":{"OTHER":"x","` + envKey + `":"1"}}`, linked: true,
				ledgerFor: map[string]map[string]string{"{store}": {envKey: "1"}},
			},
			lines:     []string{"  change  claude.env: remove " + envKey + " in {store}"},
			wantStore: `{"OTHER":"x"}`,
		},
		{
			name: "unparsable settings fail and stay untouched", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: `{not json`, linked: true},
			wantErr:   "claude.env: read {seat}",
			wantStore: "-",
		},
		{
			name: "an unreadable ledger fails before any write", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: `{}`, linked: true, ledger: `{bad`},
			wantErr:   "claude.env: read the ownership ledger",
			wantStore: "-",
		},
		{
			name: "a key no plugin's prefix names warns and is still set", apply: true,
			env:   map[string]string{"NOPE_X": "1", "SUB_AGENT_COMPACT": "2"},
			given: state{store: `{}`, linked: true},
			lines: []string{
				"  warn    claude.env NOPE_X: no plugin pfm installs is named by its prefix",
				"  change  claude.env: set NOPE_X, SUB_AGENT_COMPACT in {seat}",
			},
			absent:    []string{"claude.env SUB_AGENT_COMPACT:"},
			wantStore: `{"NOPE_X":"1","SUB_AGENT_COMPACT":"2"}`,
			wantOwned: map[string]map[string]string{"{store}": {"NOPE_X": "1", "SUB_AGENT_COMPACT": "2"}},
			wantMode:  0o644,
		},
		{
			name: "a former primary seat's keys are removed", apply: true,
			env: map[string]string{envKey: "1"},
			given: state{
				store: `{}`, linked: true,
				other:     `{"env":{"` + envKey + `":"1"}}`,
				ledgerFor: map[string]map[string]string{"{other}": {envKey: "1"}},
			},
			lines: []string{
				"  change  claude.env: remove " + envKey + " in {other}",
				"  change  claude.env: set " + envKey + " in {seat}",
			},
			wantStore: `{"` + envKey + `":"1"}`,
			wantOther: "",
			wantOwned: map[string]map[string]string{"{store}": {envKey: "1"}},
			wantMode:  0o644,
		},
		{
			name: "a hand-set key holding the wanted value stays the user's", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: `{"env":{"` + envKey + `":"1"}}`, linked: true},
			lines:     []string{"  ok      claude.env: 1 keys in {seat}"},
			absent:    []string{"  change  "},
			wantStore: "-",
		},
		{
			name: "a whitespace-only settings file is an empty env", apply: true,
			env:       map[string]string{envKey: "1"},
			given:     state{store: " \n", linked: true},
			lines:     []string{"  change  claude.env: set " + envKey + " in {seat}"},
			wantStore: `{"` + envKey + `":"1"}`,
			wantOwned: map[string]map[string]string{"{store}": {envKey: "1"}},
			wantMode:  0o644,
		},
		{
			name: "an empty claude.env removing owned keys checks no prefix", apply: true,
			unreadPlugins: true,
			env:           map[string]string{},
			given: state{
				store: `{"env":{"` + envKey + `":"1"}}`, linked: true,
				ledgerFor: map[string]map[string]string{"{store}": {envKey: "1"}},
			},
			lines:     []string{"  change  claude.env: remove " + envKey + " in {seat}"},
			absent:    []string{"prefix was checked"},
			wantStore: "",
		},
		{
			name: "empty claude.env and no ledger read nothing", apply: true,
			env:       map[string]string{},
			given:     state{store: `{not json`, linked: true},
			lines:     []string{"  ok      claude.env: empty, and pfm set no key earlier"},
			wantStore: "-",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			store := filepath.Join(home, ".claude", "settings.json")
			first := filepath.Join(home, ".cc", "1")
			seat := filepath.Join(first, "settings.json")
			other := filepath.Join(home, ".cc", "9", "settings.json")
			if err := os.MkdirAll(first, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.given.store != "" {
				writeFixture(t, store, test.given.store)
				if err := os.Chmod(store, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.given.linked {
				if err := os.Symlink(store, seat); err != nil {
					t.Fatal(err)
				}
			}
			if test.given.other != "" {
				writeFixture(t, other, test.given.other)
			}
			expand := func(text string) string {
				return strings.NewReplacer(
					"{seatfile}", seat, "{seat}", seat, "{store}", store, "{other}", other,
				).Replace(text)
			}
			expandOwned := func(owned map[string]map[string]string) map[string]map[string]string {
				if owned == nil {
					return nil
				}
				out := map[string]map[string]string{}
				for path, keys := range owned {
					out[expand(path)] = keys
				}
				return out
			}
			managedRoot := managedRootForHome(home)
			ledgerPath := filepath.Join(managedRoot, claudeEnvOwnershipName)
			switch {
			case test.given.ledger != "":
				writeFixture(t, ledgerPath, test.given.ledger)
			case test.given.ledgerFor != nil:
				raw, err := json.Marshal(claudeEnvOwnership{Version: 1, Files: expandOwned(test.given.ledgerFor)})
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, ledgerPath, string(raw))
			}
			before := map[string]string{}
			for _, path := range []string{store, other, ledgerPath} {
				if raw, err := os.ReadFile(path); err == nil {
					before[path] = string(raw)
				}
			}
			var out bytes.Buffer
			inst := &engine{options: Options{
				Home:             home,
				ConfigDir:        filepath.Join(home, ".claude"),
				PrimaryConfigDir: first,
				Version:          "0.80.0",
				ClaudePlugins:    pfmconfig.DefaultClaudePlugins(),
				ClaudeEnv:        test.env,
				Stdout:           &out,
			}, apply: test.apply, managedRoot: managedRoot}
			if test.unreadPlugins {
				inst.options.Version, inst.options.SourceRepo = "0.80.0-alpha", home
				inst.options.ClaudePluginsDefault = true
			}
			err := inst.ensureClaudeEnv(test.uninstall)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), expand(test.wantErr)) {
					t.Fatalf("err = %v, want %q\n%s", err, expand(test.wantErr), out.String())
				}
				if !strings.Contains(out.String(), "  FAIL    ") {
					t.Fatalf("no FAIL line:\n%s", out.String())
				}
			} else if err != nil {
				t.Fatalf("ensureClaudeEnv: %v\n%s", err, out.String())
			}
			for _, line := range test.lines {
				if !strings.Contains(out.String(), expand(line)+"\n") {
					t.Fatalf("missing %q:\n%s", expand(line), out.String())
				}
			}
			for _, text := range test.absent {
				if strings.Contains(out.String(), text) {
					t.Fatalf("output holds %q:\n%s", text, out.String())
				}
			}
			assertEnv := func(path, want string) {
				t.Helper()
				raw, err := os.ReadFile(path)
				if want == "-" {
					if string(raw) != before[path] {
						t.Fatalf("%s was written: %s", path, raw)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var document map[string]any
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				env, has := document["env"]
				if want == "" {
					if has {
						t.Fatalf("%s keeps env %v", path, env)
					}
					return
				}
				var wantEnv map[string]any
				if err := json.Unmarshal([]byte(want), &wantEnv); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(env, wantEnv) {
					t.Fatalf("%s env = %v, want %v", path, env, wantEnv)
				}
			}
			if test.given.store != "" {
				assertEnv(store, test.wantStore)
				if strings.Contains(before[store], "9007199254740993") &&
					!strings.Contains(readFixture(t, store), "9007199254740993") {
					t.Fatalf("settings lost a number: %s", readFixture(t, store))
				}
			}
			if test.given.other != "" {
				assertEnv(other, test.wantOther)
			}
			if test.given.linked {
				if info, err := os.Lstat(seat); err != nil || info.Mode()&fs.ModeSymlink == 0 {
					t.Fatalf("the seat's settings link was replaced: %v %v", info, err)
				}
			}
			if test.wantMode != 0 {
				if info, err := os.Stat(store); err != nil || info.Mode().Perm() != test.wantMode {
					t.Fatalf("store mode = %v %v, want %v", info, err, test.wantMode)
				}
			}
			if test.given.store == "" && test.apply && test.wantErr == "" {
				info, err := os.Lstat(seat)
				if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
					t.Fatalf("created settings = %v %v, want a 0600 file", info, err)
				}
				assertEnv(seat, `{"`+envKey+`":"1"}`)
			}
			raw, err := os.ReadFile(ledgerPath)
			switch want := expandOwned(test.wantOwned); {
			case !test.apply || test.wantErr != "":
				if string(raw) != before[ledgerPath] {
					t.Fatalf("ledger was written: %s", raw)
				}
			case want == nil:
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("ledger left behind: %s %v", raw, err)
				}
			default:
				var got claudeEnvOwnership
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("ledger: %v %s", err, raw)
				}
				if got.Version != 1 || !reflect.DeepEqual(got.Files, want) {
					t.Fatalf("ledger = %+v, want %v", got, want)
				}
				if info, err := os.Stat(ledgerPath); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("ledger mode = %v %v", info, err)
				}
			}
			if test.apply && test.wantErr == "" && !test.uninstall {
				out.Reset()
				if err := inst.ensureClaudeEnv(false); err != nil || strings.Contains(out.String(), "  change  ") {
					t.Fatalf("second run err=%v:\n%s", err, out.String())
				}
			}
		})
	}
}
