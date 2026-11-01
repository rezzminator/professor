package hookentry

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func pcmGuardPythonWrites(code string) ([]string, bool) {
	var targets []string
	masked := pcmGuardPyMask(code) // calls are found outside strings, read in code
	for _, m := range pcmGuardPyOpen.FindAllStringSubmatchIndex(masked, -1) {
		found, ok := pcmGuardPyOpenCall(code, m[3])
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	pathlib := strings.Contains(masked, "Path") || strings.Contains(masked, "pathlib")
	for _, m := range pcmGuardPyAttr.FindAllStringSubmatchIndex(masked, -1) {
		name, receiver := code[m[2]:m[3]], pcmGuardReceiver(code, m[0])
		var found []string
		ok := true
		switch {
		case pcmGuardPyModules[receiver] != nil:
			if n, hit := pcmGuardPyModules[receiver][name]; hit {
				var groups [][]string
				groups, ok = pcmGuardPyPathGroups(code, m[3], n)
				found = slices.Concat(groups...)
				if into, copying := pcmGuardPyCopies[receiver+"."+name]; ok && copying {
					found = pcmGuardCopyTargets(groups, into)
				}
			}
		case name == pcmGuardOpen && slices.Contains(pcmGuardPyOpeners, receiver),
			name == "ZipFile" && receiver == "zipfile", name == "FileIO" && receiver == "io":
			found, ok = pcmGuardPyOpenCall(code, m[3])
		case name == pcmGuardOpen:
			found, ok = pcmGuardPyOpenCall(code, m[3], receiver)
		default:
			n, hit := pcmGuardPyPathMethods[name]
			if !hit && pathlib {
				n, hit = pcmGuardPyRenames[name]
			}
			if hit {
				found, ok = pcmGuardPyPaths(code, m[3], n, receiver)
			}
		}
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	return targets, true
}

// pcmGuardCopyTargets keeps a copy's destinations, the second argument's
// values, the source being read; into adds each dest and source name pair for
// codeInto. groups holds each argument's values apart, since a name bound
// more than once holds several.
func pcmGuardCopyTargets(groups [][]string, into bool) []string {
	if len(groups) < 2 {
		return slices.Concat(groups...)
	}
	out := slices.Clone(groups[1])
	if into {
		for _, dest := range groups[1] {
			for _, source := range groups[0] {
				out = append(out, dest+pcmGuardInto+source)
			}
		}
	}
	return out
}

// pcmGuardPyPaths reads the call after code[:end]: lead (a receiver) and its
// first n positional arguments are the paths it writes.
func pcmGuardPyPaths(code string, end, n int, lead ...string) ([]string, bool) {
	groups, ok := pcmGuardPyPathGroups(code, end, n, lead...)
	return slices.Concat(groups...), ok
}

// pcmGuardPyPathGroups is pcmGuardPyPaths with each argument's values kept
// apart, in argument order.
func pcmGuardPyPathGroups(code string, end, n int, lead ...string) ([][]string, bool) {
	args, ok := pcmGuardCallAt(code, end, pcmGuardPython)
	if !ok {
		return nil, false
	}
	positional, _, ok := pcmGuardKeywords(args, pcmGuardPyKeyword)
	if !ok || len(positional) < n {
		return nil, false
	}
	groups := make([][]string, 0, len(lead)+n)
	for _, expr := range append(lead, positional[:n]...) {
		values, ok := pcmGuardPyValues(code, []string{expr})
		if !ok {
			return nil, false
		}
		groups = append(groups, values)
	}
	return groups, true
}

// pcmGuardPyOpenCall reads an open call after code[:end]: its target when
// its mode writes (w, a, x or +), none for a read. With a receiver
// (Path.open) the receiver is the target and the mode comes first.
func pcmGuardPyOpenCall(code string, end int, receiver ...string) ([]string, bool) {
	args, ok := pcmGuardCallAt(code, end, pcmGuardPython)
	if !ok {
		return nil, false
	}
	positional, keywords, ok := pcmGuardKeywords(args, pcmGuardPyKeyword)
	if !ok {
		return nil, false
	}
	modeAt, target := 0, ""
	if len(receiver) > 0 && pcmGuardForeignOpen(positional) {
		receiver = nil // Image.open(path): a path first, no Path receiver
	}
	if len(receiver) > 0 {
		target = receiver[0]
	} else {
		modeAt, target = 1, cmp.Or(keywords["file"], keywords["filename"], keywords["name"])
		if len(positional) > 0 {
			target = positional[0]
		}
	}
	mode, has := keywords["mode"]
	if !has && len(positional) > modeAt {
		mode, has = positional[modeAt], true
	}
	if !has {
		return nil, true
	}
	value, ok := pcmGuardLiteral(pcmGuardPython, mode)
	if !ok {
		return nil, false
	}
	if !strings.ContainsAny(value, "wax+") {
		return nil, true
	}
	return pcmGuardPyValues(code, []string{target})
}

// pcmGuardForeignOpen reports an X.open call whose first argument is a
// literal no open mode spells, so it is a path: X is no pathlib Path.
func pcmGuardForeignOpen(positional []string) bool {
	if len(positional) == 0 {
		return false
	}
	value, ok := pcmGuardLiteral(pcmGuardPython, positional[0])
	return ok && (value == "" || strings.Trim(value, "rwxabtU+") != "")
}

// pcmGuardPyValues reads each expression as the paths it can hold; a name
// bound only by plain assignments holds each of their values in turn.
func pcmGuardPyValues(code string, exprs []string) ([]string, bool) {
	values := make([]string, 0, len(exprs))
	for _, expr := range exprs {
		if name := strings.TrimSpace(expr); pcmGuardIdent.MatchString(name) {
			bound, ok := pcmGuardPyBindings(code, name, 0)
			if !ok {
				return nil, false
			}
			values = append(values, bound...)
			continue
		}
		value, ok := pcmGuardPyValue(code, expr, 0)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

// pcmGuardPyValue reads a python expression as a path: a plain string
// literal, Path(...) or os.path.join(...) over such values, or a name bound
// exactly once in the code, by a plain assignment, to one.
func pcmGuardPyValue(code, expr string, depth int) (string, bool) {
	expr = strings.TrimSpace(expr)
	if value, ok := pcmGuardLiteral(pcmGuardPython, expr); ok {
		return value, true
	}
	if depth > pcmGuardMaxValueDepth {
		return "", false
	}
	if pcmGuardIdent.MatchString(expr) {
		return pcmGuardPyBinding(code, expr, depth)
	}
	paren := strings.IndexByte(expr, '(')
	if paren < 0 || !slices.Contains(pcmGuardPyPathCalls, strings.Join(strings.Fields(expr[:paren]), "")) ||
		pcmGuardClose(expr, paren+1, pcmGuardPython) != len(expr)-1 {
		return "", false
	}
	joined := ""
	for _, arg := range pcmGuardSplitTop(expr[paren+1:len(expr)-1], pcmGuardPython) {
		part, ok := pcmGuardPyValue(code, arg, depth+1)
		if !ok {
			return "", false
		}
		if joined == "" || filepath.IsAbs(part) {
			joined = part
		} else {
			joined = filepath.Join(joined, part)
		}
	}
	return joined, joined != ""
}

// pcmGuardPyBinding follows name to the value of its one binding.
func pcmGuardPyBinding(code, name string, depth int) (string, bool) {
	values, ok := pcmGuardPyBindings(code, name, depth)
	if !ok || len(values) != 1 {
		return "", false
	}
	return values[0], true
}

// pcmGuardPyBindings follows name to the value of each of its bindings. Every
// way python binds a name counts, over-counted rather than missed; a binding
// other than `name = value`, or none, leaves it unread.
func pcmGuardPyBindings(code, name string, depth int) ([]string, bool) {
	quoted := regexp.QuoteMeta(name)
	word := regexp.MustCompile(`\b` + quoted + `\b`)
	assigned := 0
	var rhs []string
	statements := pcmGuardStatements(pcmGuardPyDecomment(code))
	for _, stmt := range statements {
		eqs := pcmGuardAssigns(stmt)
		if len(eqs) == 0 {
			continue
		}
		lhs := stmt[:eqs[len(eqs)-1]]
		hits := len(word.FindAllStringIndex(lhs, -1))
		assigned += hits
		if hits == 1 && len(eqs) == 1 && strings.TrimSpace(lhs) == name {
			rhs = append(rhs, stmt[eqs[0]+1:])
		}
	}
	// Text-wide, an assignment the statement split may have missed still counts.
	bound := max(assigned, len(pcmGuardBinder(pcmGuardPyAssign, quoted).FindAllStringIndex(code, -1)))
	// A bracketed statement may span lines (def f(\n p):), so each binder is
	// also counted on the statements with their inner newlines flattened.
	flat := strings.ReplaceAll(strings.Join(statements, "\x00"), "\n", " ")
	flat = strings.ReplaceAll(flat, "\x00", "\n")
	for _, binder := range pcmGuardPyBinders {
		re := pcmGuardBinder(binder, quoted)
		bound += max(len(re.FindAllStringIndex(code, -1)), len(re.FindAllStringIndex(flat, -1)))
	}
	if bound == 0 || bound != len(rhs) {
		return nil, false
	}
	values := make([]string, 0, len(rhs))
	for _, each := range rhs {
		value, ok := pcmGuardPyValue(code, each, depth+1)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

// pcmGuardPyMask blanks the text of python's comments and of its strings but
// f-strings, whose braces are code, keeping every offset: prose in a string
// ("see [a](b)") is no call or escape.
func pcmGuardPyMask(code string) string {
	out := []byte(code)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if out[k] != '\n' && out[k] != '\'' && out[k] != '"' {
				out[k] = ' '
			}
		}
	}
	for i := 0; i < len(code); {
		switch code[i] {
		case '\'', '"':
			j := pcmGuardSkipQuote(code, i, true)
			if !pcmGuardFString(code, i) {
				blank(i+1, j)
			}
			i = j
		case '#':
			j := strings.IndexByte(code[i:], '\n')
			if j < 0 {
				j = len(code) - i
			}
			blank(i, i+j)
			i += j
		default:
			i++
		}
	}
	return string(out)
}

// pcmGuardFString reports a string literal opening at code[quote] whose
// prefix (rb, Rf, …) holds an f.
func pcmGuardFString(code string, quote int) bool {
	start := quote
	for start > 0 && strings.IndexByte("rRbBuUfF", code[start-1]) >= 0 {
		start--
	}
	return strings.ContainsAny(code[start:quote], "fF")
}

func pcmGuardBinder(pattern, quoted string) *regexp.Regexp {
	return regexp.MustCompile(strings.ReplaceAll(pattern, "NAME", quoted))
}

// pcmGuardPyDecomment drops python's # comments, strings kept whole.
func pcmGuardPyDecomment(code string) string {
	var b strings.Builder
	for i := 0; i < len(code); {
		switch c := code[i]; c {
		case '\'', '"':
			j := pcmGuardSkipQuote(code, i, true)
			b.WriteString(code[i:j])
			i = j
		case '#':
			j := strings.IndexByte(code[i:], '\n')
			if j < 0 {
				return b.String()
			}
			i += j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// pcmGuardStatements splits python code at its top-level newlines and
// semicolons.
func pcmGuardStatements(code string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(code); i++ {
		switch c := code[i]; {
		case c == '\'' || c == '"':
			i = pcmGuardSkipQuote(code, i, true) - 1
		case strings.IndexByte("([{", c) >= 0:
			depth++
		case strings.IndexByte(")]}", c) >= 0:
			depth = max(depth-1, 0)
		case depth == 0 && (c == '\n' || c == ';'):
			out = append(out, code[start:i])
			start = i + 1
		}
	}
	return append(out, code[start:])
}

// pcmGuardAssigns returns where a python statement assigns at top level: =
// and every augmented op=, never ==, !=, <=, >=, := or a keyword argument.
func pcmGuardAssigns(stmt string) []int {
	var out []int
	depth := 0
	for i := 0; i < len(stmt); i++ {
		switch c := stmt[i]; {
		case c == '\'' || c == '"':
			i = pcmGuardSkipQuote(stmt, i, true) - 1
		case strings.IndexByte("([{", c) >= 0:
			depth++
		case strings.IndexByte(")]}", c) >= 0:
			depth = max(depth-1, 0)
		case c == '=' && depth == 0:
			if i+1 < len(stmt) && stmt[i+1] == '=' {
				i++
				continue
			}
			shift := i > 1 && (stmt[i-2:i] == "<<" || stmt[i-2:i] == ">>")
			if i > 0 && strings.IndexByte("!<>=:", stmt[i-1]) >= 0 && !shift {
				continue
			}
			out = append(out, i)
		}
	}
	return out
}

// values; ok is false for a splat, which could carry either.
func pcmGuardKeywords(args []string, keyword *regexp.Regexp) ([]string, map[string]string, bool) {
	var positional []string
	keywords := map[string]string{}
	for _, arg := range args {
		if strings.HasPrefix(strings.TrimSpace(arg), "*") {
			return nil, nil, false
		}
		if m := keyword.FindStringSubmatch(arg); m != nil {
			keywords[m[1]] = m[2]
			continue
		}
		positional = append(positional, arg)
	}
	return positional, keywords, true
}

// pcmGuardLiterals reads every expression as a plain literal of lang.
func pcmGuardLiterals(lang string, exprs []string) ([]string, bool) {
	values := make([]string, 0, len(exprs))
	for _, expr := range exprs {
		value, ok := pcmGuardLiteral(lang, expr)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

// pcmGuardLiteral reads text as one plain string literal of lang: no
// interpolation (an f-string, ${, perl's $ or @, ruby's #) and no escape.
func pcmGuardLiteral(lang, text string) (string, bool) {
	text = strings.TrimSpace(text)
	if lang == pcmGuardPython {
		prefix := len(text) - len(strings.TrimLeft(text, "rRbBuU"))
		if prefix > 2 {
			return "", false
		}
		text = text[prefix:]
	}
	if len(text) < 2 || strings.IndexByte(pcmGuardQuotes, text[0]) < 0 || (text[0] == '`' && lang != pcmGuardNode) {
		return "", false
	}
	q := text[:1]
	body := text[1 : len(text)-1]
	if triple := strings.Repeat(q, 3); lang == pcmGuardPython && len(text) >= 6 &&
		strings.HasPrefix(text, triple) && strings.HasSuffix(text, triple) {
		body = text[3 : len(text)-3]
	}
	switch {
	case !strings.HasSuffix(text, q), strings.Contains(body, q), strings.Contains(body, `\`),
		q == "`" && strings.Contains(body, "${"),
		lang == pcmGuardPerl && q == `"` && strings.ContainsAny(body, "$@"),
		lang == pcmGuardRuby && q == `"` && strings.Contains(body, "#"):
		return "", false
	}
	return body, true
}

// pcmGuardCallAt returns the arguments of the bracketed call right after
// code[:end]; ok is false when none follows or it never closes.
func pcmGuardCallAt(code string, end int, lang string) ([]string, bool) {
	i := end + len(code[end:]) - len(strings.TrimLeft(code[end:], " \t"))
	if i >= len(code) || code[i] != '(' {
		return nil, false
	}
	closeAt := pcmGuardClose(code, i+1, lang)
	if closeAt < 0 {
		return nil, false
	}
	return pcmGuardSplitTop(code[i+1:closeAt], lang), true
}

// pcmGuardLooseArgs returns the arguments of perl's or ruby's call after
// code[:end], bracketed or not: unbracketed, they run to the end of the
// statement, a block or a low-precedence operator. An unclosed bracket
// gives none, which no write accepts.
func pcmGuardLooseArgs(code string, end int, lang string) []string {
	if args, ok := pcmGuardCallAt(code, end, lang); ok {
		return args
	}
	if rest := strings.TrimLeft(code[end:], " \t"); strings.HasPrefix(rest, "(") {
		return nil
	}
	stop, _ := pcmGuardTopLevel(code, end, lang, func(i int) bool { return pcmGuardBareEnd(code[i:]) })
	return pcmGuardSplitTop(code[end:stop], lang)
}

// pcmGuardBareEnd reports whether an unbracketed call's arguments end where
// rest starts.
func pcmGuardBareEnd(rest string) bool {
	if strings.IndexByte(";\n{#", rest[0]) >= 0 || strings.HasPrefix(rest, "||") || strings.HasPrefix(rest, "&&") {
		return true
	}
	if rest[0] != ' ' && rest[0] != '\t' {
		return false
	}
	word := strings.TrimLeft(rest, " \t")
	for _, op := range pcmGuardLowOps {
		if after, ok := strings.CutPrefix(word, op); ok && (after == "" || !pcmGuardWordByte(after[0])) {
			return true
		}
	}
	return false
}

func pcmGuardWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// pcmGuardClose returns the index of the bracket closing the one opened just
// before from, or -1.
func pcmGuardClose(code string, from int, lang string) int {
	i, closer := pcmGuardTopLevel(code, from, lang, nil)
	if !closer {
		return -1
	}
	return i
}

// pcmGuardSplitTop splits text at its top-level commas; a blank last part
// (an empty list, a trailing comma) is dropped.
func pcmGuardSplitTop(text, lang string) []string {
	var parts []string
	for start := 0; ; {
		i, _ := pcmGuardTopLevel(text, start, lang, func(i int) bool { return text[i] == ',' })
		parts = append(parts, text[start:i])
		if i >= len(text) || text[i] != ',' {
			break
		}
		start = i + 1
	}
	if strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// pcmGuardTopLevel walks code from i, quotes and brackets skipped, to the
// first top-level byte stop accepts (closer false) or the first unmatched
// closing bracket (closer true); len(code) when neither comes.
func pcmGuardTopLevel(code string, i int, lang string, stop func(int) bool) (int, bool) {
	depth := 0
	for i < len(code) {
		c := code[i]
		switch {
		case strings.IndexByte(pcmGuardQuotes, c) >= 0:
			i = pcmGuardSkipQuote(code, i, lang == pcmGuardPython)
			continue
		case strings.IndexByte("([{", c) >= 0:
			depth++
		case strings.IndexByte(")]}", c) >= 0:
			if depth == 0 {
				return i, true
			}
			depth--
		case depth == 0 && stop != nil && stop(i):
			return i, false
		}
		i++
	}
	return len(code), false
}

// pcmGuardSkipQuote returns the index just past the string literal opening
// at code[i] (a python triple quote when triple), backslash escapes skipped;
// len(code) when it never closes.
func pcmGuardSkipQuote(code string, i int, triple bool) int {
	q := code[i]
	if fence := strings.Repeat(string(q), 3); triple && strings.HasPrefix(code[i:], fence) {
		if end := strings.Index(code[i+3:], fence); end >= 0 {
			return i + 3 + end + 3
		}
		return len(code)
	}
	for j := i + 1; j < len(code); j++ {
		switch code[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(code)
}

// pcmGuardReceiver returns the expression the attribute at code[dot] is read
// on: names, dots and the bracketed calls, indexes and strings between them;
// "" when a bracket or quote does not match.
func pcmGuardReceiver(code string, dot int) string {
	end := len(strings.TrimRight(code[:dot], " \t"))
	i := end
	for i > 0 {
		switch c := code[i-1]; {
		case pcmGuardWordByte(c) || c == '.':
			i--
		case c == ')' || c == ']':
			if i = pcmGuardOpenBefore(code, i-1); i < 0 {
				return ""
			}
		case c == '\'' || c == '"':
			if i = strings.LastIndexByte(code[:i-1], c); i < 0 {
				return ""
			}
		default:
			return code[i:end]
		}
	}
	return code[i:end]
}

// pcmGuardOpenBefore returns the index of the bracket opening the one closed
// at code[closeAt], quotes skipped backward, or -1.
func pcmGuardOpenBefore(code string, closeAt int) int {
	depth := 0
	for i := closeAt - 1; i >= 0; i-- {
		switch c := code[i]; {
		case c == '\'' || c == '"':
			if i = strings.LastIndexByte(code[:i], c); i < 0 {
				return -1
			}
		case strings.IndexByte(")]}", c) >= 0:
			depth++
		case strings.IndexByte("([{", c) >= 0:
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}
