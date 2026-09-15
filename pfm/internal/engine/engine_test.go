package engine

import (
	"strings"
	"testing"
)

func TestParseAcceptsShortAndLongSpellings(t *testing.T) {
	const spacedCodex = " codex "
	tests := map[string]ID{
		"cc":        Claude,
		"Claude":    Claude,
		spacedCodex: Codex,
		"OX":        Opencode,
	}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			got, err := Parse(value)
			if err != nil || got != want {
				t.Fatalf("Parse(%q) = %q, %v; want %q, nil", value, got, err, want)
			}
		})
	}
}

func TestParseRefusesEmptyAndUnknown(t *testing.T) {
	const acceptedSet = "cc/claude, cx/codex, ox/opencode"
	for _, value := range []string{"", "bogus"} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse(value)
			if err == nil || !strings.Contains(err.Error(), acceptedSet) {
				t.Fatalf("Parse(%q) error = %v; want accepted set %q", value, err, acceptedSet)
			}
		})
	}
}

func TestEveryDescriptorIsComplete(t *testing.T) {
	for _, id := range All() {
		d := MustLookup(id)
		if d.ID == "" || d.Name == "" || d.Short == "" || d.LongName == "" ||
			d.Binary == "" || d.SocketPrefix == "" || d.RootEnv == "" {
			t.Errorf("descriptor %q is incomplete: %#v", id, d)
		}
		if d.DefaultRoots == nil || len(d.DefaultRoots("/h")) == 0 {
			t.Errorf("descriptor %q has no default roots", id)
		}
		if d.SocketPrefix != string(d.ID)+"-" {
			t.Errorf("descriptor %q socket prefix = %q, want %q", id, d.SocketPrefix, string(d.ID)+"-")
		}
		if d.LongName != strings.ToLower(d.LongName) {
			t.Errorf("descriptor %q long name = %q, want lowercase", id, d.LongName)
		}
	}
}

func TestRegisterRefusesADuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register(existing descriptor) did not panic")
		}
	}()
	Register(MustLookup(Claude))
}

func TestRegisterRefusesAmbiguousLongNamesAndSocketPrefixes(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		descriptor Descriptor
	}{
		{
			name:       "long name",
			descriptor: Descriptor{ID: "zy", Name: "Alias", Short: "Alias", LongName: "CLAUDE", Binary: "alias", SocketPrefix: "zy-", RootEnv: "PFM_ZY_ROOT", DefaultRoots: func(string) []string { return []string{"/zy"} }},
		},
		{
			name:       "socket prefix",
			descriptor: Descriptor{ID: "zz", Name: "Zed", Short: "Zed", LongName: "zed", Binary: "zed", SocketPrefix: MustLookup(Codex).SocketPrefix, RootEnv: "PFM_ZZ_ROOT", DefaultRoots: func(string) []string { return []string{"/zz"} }},
		},
		{
			name:       "socket prefix overlap",
			descriptor: Descriptor{ID: "zw", Name: "Zedward", Short: "Zedward", LongName: "zedward", Binary: "zedward", SocketPrefix: "cc-extra-", RootEnv: "PFM_ZW_ROOT", DefaultRoots: func(string) []string { return []string{"/zw"} }},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Register(%s collision) did not panic", testCase.name)
				}
			}()
			Register(testCase.descriptor)
		})
	}
}

// TestLaunchArgsForDropsAFlagTheCallerAlreadyStates is the regression for the
// silently-discarded --settings bug: the spawn doors append LaunchArgs AFTER
// the caller's own argv, and --settings is single-value, so appending the
// fleet's own pair behind a caller who already typed --settings would make
// theirs win the last-occurrence race and vanish with no error. LaunchArgsFor
// must drop the fleet's flag instead whenever the caller already states it —
// by name, `--settings=X` counting the same as `--settings X` — and leave it
// alone otherwise.
func TestLaunchArgsForDropsAFlagTheCallerAlreadyStates(t *testing.T) {
	full := MustLookup(Claude).LaunchArgs
	if len(full) != 2 || full[0] != "--settings" {
		t.Fatalf("Claude LaunchArgs = %#v, want [--settings, <payload>]", full)
	}
	for _, testCase := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "no caller flag returns the pair unchanged", args: nil, want: full},
		{name: "caller flag with a separate value suppresses it", args: []string{"--settings", "/tmp/mine.json"}, want: []string{}},
		{name: "caller flag with an = value suppresses it too", args: []string{"--settings=/tmp/mine.json"}, want: []string{}},
		{name: "a merely similar flag does not suppress", args: []string{"--settingsfoo", "x"}, want: full},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := LaunchArgsFor(Claude, testCase.args)
			if len(got) != len(testCase.want) {
				t.Fatalf("LaunchArgsFor(Claude, %#v) = %#v, want %#v", testCase.args, got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("LaunchArgsFor(Claude, %#v) = %#v, want %#v", testCase.args, got, testCase.want)
				}
			}
		})
	}
}

// TestLaunchArgsForOnAnEngineWithNoLaunchArgsIsAlwaysEmpty covers Codex, which
// carries no LaunchArgs at all: no caller argv can suppress what was never
// there, and the function must never invent something for an engine that
// declares nothing.
func TestLaunchArgsForOnAnEngineWithNoLaunchArgsIsAlwaysEmpty(t *testing.T) {
	for _, args := range [][]string{nil, {"--settings", "/tmp/mine.json"}, {"--anything"}} {
		if got := LaunchArgsFor(Codex, args); len(got) != 0 {
			t.Fatalf("LaunchArgsFor(Codex, %#v) = %#v, want empty", args, got)
		}
	}
}

func TestFromSocketRecognizesEveryEngine(t *testing.T) {
	for _, id := range All() {
		name := MustLookup(id).SocketPrefix + "session"
		got, ok := FromSocket(name)
		if !ok || got != id {
			t.Fatalf("FromSocket(%q) = %q, %t; want %q, true", name, got, ok, id)
		}
	}
	if got, ok := FromSocket("zz-session"); ok || got != "" {
		t.Fatalf("FromSocket(unknown) = %q, %t; want empty, false", got, ok)
	}
}
