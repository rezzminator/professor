package themesync

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSyntaxFamilyTakesTheFirstSelectorsRootScope(t *testing.T) {
	cases := []struct {
		scope []string
		want  string
	}{
		{[]string{"comment", "meta.var.expr storage.type"}, "comment"},
		{[]string{"comment.block.documentation variable"}, "comment"},
		{[]string{"storage.type.function"}, "keyword"},
		{[]string{"keyword.control", "comment"}, "keyword"},
		{[]string{"heading.1.markdown"}, "markup"},
		{[]string{"text.html.markdown markup.inline.raw.markdown"}, "markup"},
		{[]string{"invalid.illegal"}, "diagnostic"},
		{[]string{"token.warn-token"}, "diagnostic"},
		{[]string{"source.css variable"}, "source"},
		{[]string{"  meta.embedded  "}, "meta"},
		{[]string{"tag.decorator.js entity.name.tag.js"}, "other"},
		{[]string{"metadata"}, "other"},
		{[]string{""}, "other"},
		{nil, "other"},
	}
	for _, tc := range cases {
		if got := syntaxFamily(VSCodeTokenColor{Scope: tc.scope}); got != tc.want {
			t.Errorf("syntaxFamily(%q) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}

// TestSyntaxFamiliesClaimEachRootOnceAndEveryFamilyIsUsed holds the table
// to its rule: no root scope claimed by two families, syntaxOtherFamily not
// a table name, the table sorted, and every family holding a rule in some
// shipped theme, so no file is one every theme carries empty forever.
func TestSyntaxFamiliesClaimEachRootOnceAndEveryFamilyIsUsed(t *testing.T) {
	t.Parallel() // read-only over the shipped themes; no process state
	owners := map[string]string{}
	var names []string
	for _, family := range syntaxFamilies {
		names = append(names, family.name)
		if family.name == syntaxOtherFamily {
			t.Errorf("%s is the fallback family, not a table row", syntaxOtherFamily)
		}
		if len(family.roots) == 0 {
			t.Errorf("syntax family %s claims no root scope", family.name)
		}
		for _, root := range family.roots {
			if owner, taken := owners[root]; taken {
				t.Errorf("root %q claimed by both %s and %s", root, owner, family.name)
			}
			owners[root] = family.name
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("syntaxFamilies not sorted by name: %v", names)
	}
	themes, err := filepath.Glob(filepath.Join(shippedThemesDir, "*", "theme.json"))
	if err != nil || len(themes) == 0 {
		t.Fatalf("no shipped theme under %s (%v): nothing judged", shippedThemesDir, err)
	}
	used := map[string]bool{}
	for _, meta := range themes {
		content, err := ReadProfileDir(filepath.Dir(meta))
		if err != nil {
			t.Fatal(err)
		}
		profile, err := ParseProfile(content)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range profile.Surfaces.VSCode.Theme.TokenColors {
			used[syntaxFamily(rule)] = true
		}
	}
	for _, name := range syntaxFamilyNames() {
		if !used[name] {
			t.Errorf("syntax family %s holds no rule in any shipped theme", name)
		}
	}
}

// TestSplitSyntaxKeysRulesByOrdinalAndJoinsThemBack: each rule lands in its
// family's file under its index, and joining the files restores the order.
func TestSplitSyntaxKeysRulesByOrdinalAndJoinsThemBack(t *testing.T) {
	rules := []VSCodeTokenColor{
		{Scope: []string{"meta.a"}},
		{Scope: []string{"comment"}},
		{Scope: []string{"meta.b"}},
		{Scope: []string{"tag.x"}},
	}
	families, err := splitSyntax(rules)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(families["meta"])); !slices.Equal(got, []string{"0000", "0002"}) {
		t.Errorf("meta ordinals = %v, want [0000 0002]", got)
	}
	if _, held := families["other"]["0003"]; !held || len(families) != len(syntaxFamilyNames()) {
		t.Errorf("families = %v: want every family present and the tag rule in other under 0003", families)
	}
	joined, err := joinSyntax(families)
	if err != nil || len(joined) != len(rules) {
		t.Fatalf("joinSyntax = %v, %v", joined, err)
	}
	for index := range rules {
		if !slices.Equal(joined[index].Scope, rules[index].Scope) {
			t.Errorf("rule %d = %v, want %v", index, joined[index].Scope, rules[index].Scope)
		}
	}
	if _, err := splitSyntax(make([]VSCodeTokenColor, 10001)); err == nil ||
		!strings.Contains(err.Error(), "10001 rules") {
		t.Errorf("splitSyntax(10001 rules) = %v, want the ordinal width error", err)
	}
}
