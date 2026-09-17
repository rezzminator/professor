// Package engine is the one place pfm knows which chat engines exist. Every
// other package asks this one; none of them spells an engine name.
package engine

import (
	"fmt"
	"sort"
	"strings"
)

// ID is an engine's short code. It is the ONE spelling: stored in fleet.db,
// printed in logs, used as the tmux socket prefix, emitted in JSON.
type ID string

const (
	Claude   ID = "cc"
	Codex    ID = "cx"
	OpenCode ID = "ox"
)

// Descriptor is everything pfm knows about an engine that is DATA. Behaviour
// (how to index it, how to spawn it, how to recognise its process) lives in
// the consumer packages' registries — see the spec, § 3.
type Descriptor struct {
	ID    ID
	Name  string // "Claude Code" — the product name, for doctor and help text
	Short string // "Claude" — the one-word label pickers and the statusline show
	// Binary is the executable's base name; BinaryPathHints are substrings of
	// an executable path that also identify the engine (Claude's versioned
	// installs live under ".../claude/versions/<v>/claude").
	Binary          string
	BinaryPathHints []string
	// SocketPrefix names the tmux servers this engine's chats run on ("cc-").
	SocketPrefix string
	// SessionEnv is the variable the engine exports into a chat's shell that
	// carries the session id; "" when the engine exports none (then whoami
	// falls back to SocketPrefix). HomeEnv is the variable that relocates the
	// engine's config dir; "" when the engine has none.
	SessionEnv string
	HomeEnv    string
	// LaunchEnv holds the NAME=value assignments every launch of this engine
	// carries, whichever door starts it (action.ClaudeSpawn, headless/run).
	LaunchEnv []string
	// LaunchArgs holds the argv words every launch of this engine carries,
	// whichever door starts it (action.ClaudeSpawn, headless/run) — the argv
	// twin of LaunchEnv, for flags rather than environment assignments.
	// A flag the caller already spells for itself is dropped from this list
	// rather than appended after it — see LaunchArgsFor.
	LaunchArgs []string
	// RootEnv is the PFM_* variable a test jail sets to relocate this engine's
	// session store; DefaultRoots computes the production roots from $HOME.
	RootEnv      string
	DefaultRoots func(home string) []string
	// LongName is the spelling a human types on the CLI ("claude"); Parse
	// accepts it alongside ID.
	LongName string
}

var registry = map[ID]Descriptor{}

// Register adds a descriptor. It is called from this package's init for the
// three built-in engines and from tests that prove a fourth engine needs
// nothing else. Registering an ID twice is a programming error and panics:
// it can only happen at init time, never from user input.
func Register(d Descriptor) {
	if d.ID == "" || strings.TrimSpace(d.LongName) == "" || d.SocketPrefix == "" {
		panic(
			fmt.Sprintf(
				"engine descriptor is incomplete: id=%q long_name=%q socket_prefix=%q",
				d.ID,
				d.LongName,
				d.SocketPrefix,
			),
		)
	}
	if _, dup := registry[d.ID]; dup {
		panic(fmt.Sprintf("engine %q registered twice", d.ID))
	}
	for id := range registry {
		existing := registry[id]
		for _, candidate := range []string{string(d.ID), d.LongName} {
			for _, registered := range []string{string(id), existing.LongName} {
				if strings.EqualFold(candidate, registered) {
					panic(
						fmt.Sprintf(
							"engine %q alias %q collides with engine %q alias %q",
							d.ID,
							candidate,
							id,
							registered,
						),
					)
				}
			}
		}
		if strings.HasPrefix(d.SocketPrefix, existing.SocketPrefix) ||
			strings.HasPrefix(existing.SocketPrefix, d.SocketPrefix) {
			panic(
				fmt.Sprintf(
					"engine %q socket prefix %q collides with engine %q prefix %q",
					d.ID,
					d.SocketPrefix,
					id,
					existing.SocketPrefix,
				),
			)
		}
	}
	registry[d.ID] = d
}

// All returns every registered ID in stable order (sorted by ID).
func All() []ID {
	ids := make([]ID, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Lookup returns the descriptor for a KNOWN id. An unknown id is an error
// naming the accepted set — never a default engine.
func Lookup(id ID) (Descriptor, error) {
	d, ok := registry[id]
	if !ok {
		return Descriptor{}, fmt.Errorf("unknown engine %q (want %s)", id, accepted())
	}
	return d, nil
}

// Parse turns what a human typed, a config file holds, or a database row
// stores into an ID. It accepts the short code and the long name, case- and
// space-insensitively. Empty input is an ERROR: the caller must say which
// engine it means; defaulting to Claude is how a third engine inherited
// Claude's behaviour for a year.
func Parse(value string) (ID, error) {
	want := strings.ToLower(strings.TrimSpace(value))
	if want == "" {
		return "", fmt.Errorf("no engine given (want %s)", accepted())
	}
	for _, id := range All() {
		d := registry[id]
		longName := strings.ToLower(d.LongName)
		if want == string(d.ID) || want == longName {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("unknown engine %q (want %s)", value, accepted())
}

// accepted renders "cc/claude, cx/codex, ox/opencode" from the registry, so
// the error text is never a hand-written list that drifts.
func accepted() string {
	parts := make([]string, 0, len(registry))
	for _, id := range All() {
		parts = append(parts, string(id)+"/"+registry[id].LongName)
	}
	return strings.Join(parts, ", ")
}

// MustLookup is for call sites holding an ID that came from Parse or a
// constant. It panics on an unknown ID because that is a programming error,
// not an input error. Never call it on a string from outside the process.
func MustLookup(id ID) Descriptor {
	d, err := Lookup(id)
	if err != nil {
		panic(err)
	}
	return d
}

// FromSocket resolves an engine from its tmux socket name. Unknown socket
// prefixes are named absence, never a fallback to another engine.
func FromSocket(name string) (ID, bool) {
	for _, id := range All() {
		if strings.HasPrefix(name, registry[id].SocketPrefix) {
			return id, true
		}
	}
	return "", false
}

// LaunchArgsFor returns id's LaunchArgs with every flag the caller already
// states in callerArgs removed.
//
// The spawn doors append LaunchArgs AFTER the caller's own argv, and a
// single-value flag's last occurrence wins: appending the fleet's own
// --settings behind a user who typed `claude --settings ~/mine.json` would
// silently discard the file they asked for, with no warning and no error.
// Dropping ours instead leaves that launch exactly as it behaved before
// LaunchArgs existed, and loses nothing anyone typed. A flag is matched by
// name, so `--settings=X` counts as stated just as `--settings X` does.
func LaunchArgsFor(id ID, callerArgs []string) []string {
	launch := MustLookup(id).LaunchArgs
	if len(launch) == 0 || len(callerArgs) == 0 {
		return launch
	}
	stated := make(map[string]bool, len(callerArgs))
	for _, argument := range callerArgs {
		if !strings.HasPrefix(argument, "--") || argument == "--" {
			continue
		}
		name, _, _ := strings.Cut(argument, "=")
		stated[name] = true
	}
	kept := make([]string, 0, len(launch))
	for index := 0; index < len(launch); index++ {
		flag := launch[index]
		value, hasValue := "", index+1 < len(launch) && !strings.HasPrefix(launch[index+1], "--")
		if hasValue {
			value = launch[index+1]
			index++
		}
		if stated[flag] {
			continue
		}
		kept = append(kept, flag)
		if hasValue {
			kept = append(kept, value)
		}
	}
	return kept
}
