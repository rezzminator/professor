package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAcceptsShortAndLongSpellings(t *testing.T) {
	const spacedCodex = " codex "
	tests := map[string]ID{
		"cc":        Claude,
		"Claude":    Claude,
		spacedCodex: Codex,
		"OX":        OpenCode,
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
	for value, want := range map[string]string{"": "no engine given (want cc/claude, cx/codex, ox/opencode)", "bogus": "unknown engine \"bogus\" (want cc/claude, cx/codex, ox/opencode)"} {
		if _, err := Parse(value); fmt.Sprint(err) != want {
			t.Errorf("Parse(%q) error = %q, want %q", value, err, want)
		}
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

func TestHerdrNativePromptOnlyClaude(t *testing.T) {
	for _, id := range All() {
		if got, want := MustLookup(id).HerdrNativePrompt, id == Claude; got != want {
			t.Errorf("%s HerdrNativePrompt=%v, want %v", id, got, want)
		}
	}
}
