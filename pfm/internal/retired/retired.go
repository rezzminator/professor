// Package retired reads the clone's retired lists: every name and path an
// earlier Professor shipped that pfm still recognises, reports or removes. The
// lists live in one JSON file in the clone, read at run time, so a retirement
// is an edit to that file with no rebuild.
package retired

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// FileRel is the retired lists inside the Professor clone, slash-separated.
const FileRel = "templates/global/retired.json"

// The kinds a retired project-file name is reported as.
const (
	KindCommand = "command"
	KindAgent   = "agent"
	KindSkill   = "skill"
	KindPhase   = "phase"
	KindTool    = "tool"
	KindPath    = "path"
)

// How far past a retired name a reference may run.
const (
	// MatchExact: the name alone — `/pfm:refresh`, never `/pfm:refreshed` or
	// `/pfm:refresh:x`.
	MatchExact = "exact"
	// MatchFamily: the name alone or as a namespace — `/wave` and
	// `/wave:refine`, never `/waveform`.
	MatchFamily = "family"
	// MatchPrefix: any text the name starts — `mcp__chat__chat_inject`.
	MatchPrefix = "prefix"
)

// Lists is the whole file: one list per retirement mechanism.
type Lists struct {
	// Names are retired names a project file may still reference, reported by
	// `pfm doctor --project-updates` as RETIRED-NAME; a command name with a
	// successor also answers the retired `pfm {command}` dispatch.
	Names []Name `json:"names"`
	// StoreEntries are Claude store entries once linked from every account and
	// now per-account: install unlinks each account's link and archives the store copy.
	StoreEntries []string `json:"storeEntries"`
	// StateDirs are directories under ~/.local/state/pfm whose writers are
	// gone; install removes each whole.
	StateDirs []StateDir `json:"stateDirs"`
	// HookCommands are subcommands an older pfm registered as hooks: install
	// strips them, doctor reports them, and a retired `pfm internal {name}`
	// exits 0 silently for a session still holding the hook.
	HookCommands []HookCommand `json:"hookCommands"`
	// HookBinaries are former names of the pfm binary a hook command may start with.
	HookBinaries []string `json:"hookBinaries"`
	// HookShimHints are substrings of shell-script hooks that predate the binary.
	HookShimHints []HookShimHint `json:"hookShimHints"`
	// ClaudePlugins are plugin ids once ours, keyed by their successor's bare
	// name: doctor flags an enabled one; install disables it.
	ClaudePlugins map[string][]string `json:"claudePlugins"`
	// GlobalAgents are global Claude agent names a rename retired, removed
	// from {config}/agents/ when the file is still Professor's.
	GlobalAgents []string `json:"globalAgents"`
	// Units are systemd user units pfm renamed away from; install disables and unlinks each.
	Units []string `json:"units"`
	// ConfigFiles are files under the Claude config dir install removes; a
	// path may be a filepath.Match glob.
	ConfigFiles []ConfigFile `json:"configFiles"`
	// Commands are retired executables install removes, a customised regular
	// file backed up first.
	Commands Commands `json:"commands"`
}

// Name is one retired project-file name.
type Name struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Successor string `json:"successor,omitempty"`
	Match     string `json:"match"`
	// Probe is the root-relative file whose existence means the project defines
	// the thing itself, for a name that is a context around the thing
	// (`subagent_type: "ultimate"`); "" derives it from Name and Kind.
	Probe string `json:"probe,omitempty"`
	// Route marks a name that is also an HTTP route: a match followed by `/` or
	// `{`, or after an HTTP method, is the route, never the name.
	Route bool `json:"route,omitempty"`
}

// StateDir is one retired directory under ~/.local/state/pfm.
type StateDir struct {
	Name string `json:"name"`
	What string `json:"what"`
}

// HookCommand is one retired hook subcommand and the name it is reported under.
type HookCommand struct {
	Name       string `json:"name"`
	Subcommand string `json:"subcommand"`
}

// HookShimHint is one shell-script hook substring and the name it is reported under.
type HookShimHint struct {
	Name string `json:"name"`
	Hint string `json:"hint"`
}

// ConfigFile is one retired file under the Claude config dir.
type ConfigFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Commands are retired executables by where they lived.
type Commands struct {
	// HomeBin are names under ~/.local/bin.
	HomeBin []string `json:"homeBin"`
	// AccountBin are names under bin/ of the Claude config dir and every account.
	AccountBin []string `json:"accountBin"`
}

// ListsPath is the retired lists of the clone pfm reads (paths.GlobalSourceRepo).
func ListsPath(home string) (string, error) {
	repo, err := paths.GlobalSourceRepo(home)
	if err != nil {
		return "", fmt.Errorf("resolve the clone of %s: %w", FileRel, err)
	}
	return filepath.Join(repo, filepath.FromSlash(FileRel)), nil
}

// LoadLists reads the retired lists of the clone pfm reads; an absent or invalid
// file is an error, never empty lists.
func LoadLists(home string) (Lists, error) {
	file, err := ListsPath(home)
	if err != nil {
		return Lists{}, err
	}
	return readListsFile(file)
}

// LoadListsIfPresent is LoadLists for an answer that only names a retired
// thing: an absent file retires nothing, and an invalid one is still an error.
func LoadListsIfPresent(home string) (Lists, error) {
	lists, err := LoadLists(home)
	if errors.Is(err, fs.ErrNotExist) {
		return Lists{}, nil
	}
	return lists, err
}

// ReadLists reads the retired lists of the clone at repo.
func ReadLists(repo string) (Lists, error) {
	return readListsFile(filepath.Join(repo, filepath.FromSlash(FileRel)))
}

func readListsFile(file string) (Lists, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Lists{}, fmt.Errorf("read retired lists: %w", err)
	}
	lists, err := ParseLists(raw)
	if err != nil {
		return Lists{}, fmt.Errorf("retired lists %s: %w", file, err)
	}
	return lists, nil
}

// ParseLists decodes and validates the file: an unknown key, kind or match, an
// empty field or a path that leaves its directory is an error.
func ParseLists(raw []byte) (Lists, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var lists Lists
	if err := decoder.Decode(&lists); err != nil {
		return Lists{}, fmt.Errorf("decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Lists{}, errors.New("decode: trailing data after the object")
	}
	return lists, lists.validate()
}

func (lists Lists) validate() error {
	var problems []error
	bad := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }
	for index, name := range lists.Names {
		switch {
		case name.Name == "":
			bad("names[%d]: empty name", index)
		case !oneOf(name.Kind, KindCommand, KindAgent, KindSkill, KindPhase, KindTool, KindPath):
			bad("names[%d] %q: unknown kind %q", index, name.Name, name.Kind)
		case !oneOf(name.Match, MatchExact, MatchFamily, MatchPrefix):
			bad("names[%d] %q: unknown match %q", index, name.Name, name.Match)
		case name.Probe != "" && !relative(name.Probe):
			bad("names[%d] %q: probe %q is not a root-relative path", index, name.Name, name.Probe)
		}
	}
	for index, entry := range lists.StoreEntries {
		if !baseName(entry) {
			bad("storeEntries[%d]: %q is not one file name", index, entry)
		}
	}
	for index, dir := range lists.StateDirs {
		if !baseName(dir.Name) || dir.What == "" {
			bad("stateDirs[%d]: want a directory name and what it held, got %q, %q", index, dir.Name, dir.What)
		}
	}
	for index, hook := range lists.HookCommands {
		if hook.Name == "" || strings.TrimSpace(hook.Subcommand) == "" {
			bad("hookCommands[%d]: want a name and a subcommand, got %q, %q", index, hook.Name, hook.Subcommand)
		}
	}
	for index, binary := range lists.HookBinaries {
		if !baseName(binary) {
			bad("hookBinaries[%d]: %q is not one file name", index, binary)
		}
	}
	for index, hint := range lists.HookShimHints {
		if hint.Name == "" || hint.Hint == "" {
			bad("hookShimHints[%d]: want a name and a hint, got %q, %q", index, hint.Name, hint.Hint)
		}
	}
	for successor, ids := range lists.ClaudePlugins {
		if successor == "" || len(ids) == 0 {
			bad("claudePlugins[%q]: want a successor name and at least one retired id", successor)
		}
		for index, id := range ids {
			if !strings.Contains(id, "@") {
				bad("claudePlugins[%q][%d]: want a name@marketplace id, got %q", successor, index, id)
			}
		}
	}
	for index, agent := range lists.GlobalAgents {
		if !baseName(agent) {
			bad("globalAgents[%d]: %q is not one agent name", index, agent)
		}
	}
	for index, unit := range lists.Units {
		if !baseName(unit) {
			bad("units[%d]: %q is not one unit name", index, unit)
		}
	}
	for index, file := range lists.ConfigFiles {
		_, globErr := filepath.Match(file.Path, "")
		if !relative(file.Path) || globErr != nil || file.Reason == "" {
			bad("configFiles[%d]: want a config-relative path or glob and a reason, got %q, %q",
				index, file.Path, file.Reason)
		}
	}
	for field, names := range map[string][]string{
		"commands.homeBin": lists.Commands.HomeBin, "commands.accountBin": lists.Commands.AccountBin,
	} {
		for index, name := range names {
			if !baseName(name) {
				bad("%s[%d]: %q is not one file name", field, index, name)
			}
		}
	}
	return errors.Join(problems...)
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// relative: a clean slash-separated path that stays inside its root.
func relative(name string) bool {
	return name != "" && !path.IsAbs(name) && path.Clean(name) == name && name != ".." &&
		!strings.HasPrefix(name, "../")
}

// baseName: one path element.
func baseName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}
