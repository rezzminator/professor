package themesync

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// syntaxFamilies sorts a profile's tokenColors rules into the files of its
// directory's vscode/syntax/: each family is one {name}.json there, holding
// every rule whose first scope selector starts with a TextMate root scope
// its row names (syntaxFamily); syntaxOtherFamily holds every rule no
// family claims. The table is the whole rule, one "{family}: {roots}" row
// per family: a root scope joins a family here.
var syntaxFamilies = parseSyntaxFamilies(
	"comment: comment",
	"constant: constant",
	"diagnostic: invalid log token",
	"entity: entity",
	"keyword: keyword storage",
	"markup: fenced_code heading markup text",
	"meta: meta",
	"punctuation: punctuation",
	"source: source",
	"string: string",
	"support: support",
	"variable: variable",
)

// syntaxFamilyRow is one syntaxFamilies row: the family and the TextMate
// root scopes it claims.
type syntaxFamilyRow struct {
	name  string
	roots []string
}

// parseSyntaxFamilies reads "{family}: {space-separated roots}" rows.
func parseSyntaxFamilies(rows ...string) []syntaxFamilyRow {
	families := make([]syntaxFamilyRow, 0, len(rows))
	for _, row := range rows {
		name, roots, _ := strings.Cut(row, ":")
		families = append(families, syntaxFamilyRow{name: strings.TrimSpace(name), roots: strings.Fields(roots)})
	}
	return families
}

// syntaxOtherFamily holds the rules whose root scope no syntaxFamilies
// family names.
const syntaxOtherFamily = "other"

// syntaxOrdinalDigits is an ordinal's width: a family file keys each rule by
// its zero-padded index in tokenColors, so the files sort into the order VS
// Code reads, where a later rule wins over an earlier one of equal weight.
const syntaxOrdinalDigits = 4

// syntaxFamilyNames are every vscode/syntax family, syntaxFamilies' names
// and syntaxOtherFamily, sorted.
func syntaxFamilyNames() []string {
	names := []string{syntaxOtherFamily}
	for _, family := range syntaxFamilies {
		names = append(names, family.name)
	}
	slices.Sort(names)
	return names
}

func syntaxFile(family string) string { return profileSyntaxDir + "/" + family + ".json" }

// syntaxFamily is the family a tokenColors rule belongs to: the one naming
// the root scope, the first dotted segment, of the rule's first selector
// (`comment.block.documentation variable` is `comment`), else
// syntaxOtherFamily.
func syntaxFamily(rule VSCodeTokenColor) string {
	selector := ""
	if len(rule.Scope) > 0 {
		selector = rule.Scope[0]
	}
	words := strings.Fields(selector)
	if len(words) == 0 {
		return syntaxOtherFamily
	}
	root, _, _ := strings.Cut(words[0], ".")
	for _, family := range syntaxFamilies {
		if slices.Contains(family.roots, root) {
			return family.name
		}
	}
	return syntaxOtherFamily
}

// syntaxOrdinal is index as a family file's key.
func syntaxOrdinal(index int) string {
	return fmt.Sprintf("%0*d", syntaxOrdinalDigits, index)
}

// splitSyntax is rules as its family files' contents, by family name: each
// rule keyed by its ordinal in its family's map, every family present.
func splitSyntax(rules []VSCodeTokenColor) (map[string]map[string]VSCodeTokenColor, error) {
	if limit := len(syntaxOrdinal(0)); len(rules) > 0 && len(syntaxOrdinal(len(rules)-1)) > limit {
		return nil, fmt.Errorf("tokenColors: %d rules, more than %d-digit ordinals number", len(rules), limit)
	}
	families := map[string]map[string]VSCodeTokenColor{}
	for _, name := range syntaxFamilyNames() {
		families[name] = map[string]VSCodeTokenColor{}
	}
	for index, rule := range rules {
		families[syntaxFamily(rule)][syntaxOrdinal(index)] = rule
	}
	return families, nil
}

// joinSyntax is the tokenColors list the family files hold, in ordinal
// order: every rule in its own family's file, every ordinal four digits,
// held once, and the ordinals running from 0000 without a gap. The error
// names the file and the rule.
func joinSyntax(families map[string]map[string]VSCodeTokenColor) ([]VSCodeTokenColor, error) {
	byIndex := map[int]VSCodeTokenColor{}
	holder := map[int]string{}
	for _, name := range syntaxFamilyNames() {
		file := syntaxFile(name)
		for _, ordinal := range slices.Sorted(maps.Keys(families[name])) {
			rule := families[name][ordinal]
			index, err := strconv.Atoi(ordinal)
			if err != nil || index < 0 || len(ordinal) != syntaxOrdinalDigits {
				return nil, fmt.Errorf("%s: rule %q: an ordinal is four digits, the rule's index in tokenColors",
					file, ordinal)
			}
			if owner := syntaxFamily(rule); owner != name {
				return nil, fmt.Errorf("%s: rule %q (first scope %q) belongs in %s",
					file, ordinal, strings.Join(strings.Fields(firstScope(rule)), " "), syntaxFile(owner))
			}
			if other, held := holder[index]; held {
				return nil, fmt.Errorf("%s: rule %q holds an ordinal %s already holds", file, ordinal, other)
			}
			byIndex[index], holder[index] = rule, file
		}
	}
	rules := make([]VSCodeTokenColor, len(byIndex))
	for index := range rules {
		rule, held := byIndex[index]
		if !held {
			return nil, fmt.Errorf("%s: no rule holds ordinal %q: the ordinals run from %s without a gap",
				profileSyntaxDir, syntaxOrdinal(index), syntaxOrdinal(0))
		}
		rules[index] = rule
	}
	return rules, nil
}

func firstScope(rule VSCodeTokenColor) string {
	if len(rule.Scope) == 0 {
		return ""
	}
	return rule.Scope[0]
}
