package cmdparse

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// word is one shell word evaluated against the literal variables known so
// far: ok when every part resolved. A glob metacharacter stays in the value
// as written; no glob is ever expanded.
type word struct {
	vals []string
	ok   bool
}

const maxExpansions = 64

// digits matches a decimal integer, an optional sign included.
var digits = regexp.MustCompile(`^[+-]?\d+$`)

func (p *callParser) eval(w *syntax.Word) word {
	out := word{vals: []string{""}, ok: true}
	for _, part := range w.Parts {
		p.evalPart(part, &out, false)
		if !out.ok {
			return word{}
		}
	}
	return out
}

func (p *callParser) evalPart(part syntax.WordPart, out *word, quoted bool) {
	switch x := part.(type) {
	case *syntax.Lit:
		if quoted {
			out.appendAll([]string{dquoteUnescape(x.Value)})
			return
		}
		out.appendAll([]string{unescape(x.Value)})
	case *syntax.SglQuoted:
		out.appendAll([]string{x.Value})
	case *syntax.DblQuoted:
		for _, inner := range x.Parts {
			p.evalPart(inner, out, true)
		}
	case *syntax.ParamExp:
		vals, ok := p.param(x)
		if !ok {
			out.ok = false
			return
		}
		out.appendAll(vals)
	case *syntax.CmdSubst:
		dir, ok := p.selfDir(x)
		if !ok {
			out.ok = false
			return
		}
		out.appendAll([]string{dir})
	default:
		out.ok = false
	}
}

// partial is a script word with every part the parse knows expanded and
// every other part kept as its source, so a reader still sees the `$` where
// the value is unknown: `"$D/acks/$1.md"` run with `chat` is `$D/acks/chat.md`.
func (p *callParser) partial(parts []syntax.WordPart, quoted bool) string {
	var b strings.Builder
	for _, part := range parts {
		switch x := part.(type) {
		case *syntax.Lit:
			if quoted {
				b.WriteString(dquoteUnescape(x.Value))
			} else {
				b.WriteString(unescape(x.Value))
			}
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			b.WriteString(p.partial(x.Parts, true))
		default:
			one := word{vals: []string{""}, ok: true}
			p.evalPart(part, &one, quoted)
			if one.ok && len(one.vals) == 1 && !strings.ContainsAny(one.vals[0], "$`") {
				b.WriteString(one.vals[0])
			} else {
				b.WriteString(p.source(part))
			}
		}
	}
	return b.String()
}

// selfDir evaluates the two command substitutions a script uses to find a
// directory: `$(dirname [--] W)` and `$(cd W && pwd [-P|-L])`, W a word the
// parse knows; any other substitution, and every one in a typed command, is
// unknown.
func (p *callParser) selfDir(x *syntax.CmdSubst) (string, bool) {
	if !p.script || len(x.Stmts) != 1 {
		return "", false
	}
	stmt := x.Stmts[0]
	if stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return "", false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		operand, ok := p.callOperand(cmd, "dirname")
		if !ok {
			return "", false
		}
		if trimmed := strings.TrimRight(operand, "/"); trimmed != "" {
			operand = trimmed
		}
		return filepath.Dir(operand), true
	case *syntax.BinaryCmd:
		if cmd.Op != syntax.AndStmt || cmd.X.Negated || cmd.Y.Negated ||
			len(cmd.X.Redirs)+len(cmd.Y.Redirs) > 0 {
			return "", false
		}
		cd, isCall := cmd.X.Cmd.(*syntax.CallExpr)
		pwd, isPwd := cmd.Y.Cmd.(*syntax.CallExpr)
		if !isCall || !isPwd || len(pwd.Assigns) > 0 || len(pwd.Args) == 0 || len(pwd.Args) > 2 ||
			pwd.Args[0].Lit() != "pwd" || (len(pwd.Args) == 2 && pwd.Args[1].Lit() != "-P" && pwd.Args[1].Lit() != "-L") {
			return "", false
		}
		// Only an absolute target: the script may have cd'd before this.
		target, ok := p.callOperand(cd, "cd")
		if !ok || !filepath.IsAbs(target) {
			return "", false
		}
		return filepath.Clean(target), true
	}
	return "", false
}

// callOperand is the one operand of `program [--] W` when W evaluates to one
// value; ok is false for any other shape.
func (p *callParser) callOperand(cmd *syntax.CallExpr, program string) (string, bool) {
	if len(cmd.Assigns) > 0 || len(cmd.Args) < 2 || cmd.Args[0].Lit() != program {
		return "", false
	}
	args := cmd.Args[1:]
	if args[0].Lit() == "--" {
		args = args[1:]
	}
	if len(args) != 1 {
		return "", false
	}
	ev := p.eval(args[0])
	if !ev.ok || len(ev.vals) != 1 || ev.vals[0] == "" {
		return "", false
	}
	return ev.vals[0], true
}

// appendAll appends every suffix to every value so far.
func (w *word) appendAll(suffixes []string) {
	if !w.ok {
		return
	}
	next := make([]string, 0, len(w.vals)*len(suffixes))
	for _, v := range w.vals {
		for _, s := range suffixes {
			next = append(next, v+s)
		}
	}
	if len(next) > maxExpansions {
		w.ok = false
		return
	}
	w.vals = next
}

// param expands a parameter to its values.
func (p *callParser) param(x *syntax.ParamExp) ([]string, bool) {
	if x.Param == nil || x.Excl || x.Length || x.Width || x.Slice != nil ||
		x.Repl != nil || x.Exp != nil || x.Names != 0 {
		return nil, false
	}
	if elems, isArray := p.arrays[x.Param.Value]; isArray {
		return arrayElems(elems, x.Index)
	}
	if x.Index != nil {
		return nil, false
	}
	vals, ok := p.vars[x.Param.Value]
	return vals, ok
}

func (p *callParser) source(node syntax.Node) string {
	var buf bytes.Buffer
	if err := p.printer.Print(&buf, node); err != nil {
		return fmt.Sprintf("<unprintable: %v>", err)
	}
	return buf.String()
}

// arg is one argument as the program sees it; a word that expands to
// several values (a brace list, a loop variable, an array) is several args.
// resolved is false when the value is not known, and text then holds the
// source.
type arg struct {
	text     string
	resolved bool
}

func (p *callParser) args(w *syntax.Word) []arg {
	var out []arg
	for _, bw := range braces(w) {
		ev := p.eval(bw)
		if !ev.ok && p.script {
			return []arg{{text: p.partial(w.Parts, false)}}
		}
		if !ev.ok {
			return []arg{{text: p.source(w)}}
		}
		for _, v := range ev.vals {
			out = append(out, arg{text: v, resolved: true})
		}
	}
	return out
}

// braces brace-expands an argument word as bash does before any other
// expansion: `{a,b}.go` is a.go and b.go. The word itself is left untouched.
// A sequence (`{1..9}`) or a product over maxExpansions words stays one
// literal word.
func braces(w *syntax.Word) []*syntax.Word {
	split := &syntax.Word{Parts: slices.Clone(w.Parts)}
	if !syntax.SplitBraces(split) {
		return []*syntax.Word{w}
	}
	product := 1
	if !braceBound(split, &product) {
		return []*syntax.Word{w}
	}
	var out []*syntax.Word
	for word, err := range expand.BracesSeq(nil, split) {
		if err != nil {
			return []*syntax.Word{w}
		}
		out = append(out, word)
	}
	return out
}

// braceBound multiplies product by every brace list's width, nested ones
// included (an upper bound on the words it expands to), and reports false on a
// sequence or once the bound passes maxExpansions. syntax.Walk does not visit
// a BraceExp, hence the walk of its own.
func braceBound(w *syntax.Word, product *int) bool {
	for _, part := range w.Parts {
		br, isBrace := part.(*syntax.BraceExp)
		if !isBrace {
			continue
		}
		*product *= len(br.Elems)
		if br.Sequence || *product > maxExpansions {
			return false
		}
		for _, elem := range br.Elems {
			if !braceBound(elem, product) {
				return false
			}
		}
	}
	return true
}

func argTexts(args []arg) []string {
	texts := make([]string, 0, len(args))
	for _, a := range args {
		texts = append(texts, a.text)
	}
	return texts
}

// assignArray keeps NAME=(a b c) when every element is literal, each read
// as any argument is; one element the parse cannot know, or an explicit
// [i]=v index, drops the array.
func (p *callParser) assignArray(name string, arr *syntax.ArrayExpr) {
	delete(p.vars, name)
	delete(p.arrays, name)
	var elems []string
	for _, e := range arr.Elems {
		if e.Index != nil || e.Value == nil {
			return
		}
		for _, a := range p.args(e.Value) {
			if !a.resolved {
				return
			}
			elems = append(elems, a.text)
		}
	}
	p.arrays[name] = elems
}

// arrayElems expands an array reference: [@] and [*] are every element, a
// literal [N] is that element, no index is the first. Past the end is the
// zero value, as bash expands an unset element to "".
func arrayElems(elems []string, index syntax.ArithmExpr) ([]string, bool) {
	at := func(i int) []string {
		if i < len(elems) {
			return []string{elems[i]}
		}
		return []string{""}
	}
	if index == nil {
		return at(0), true
	}
	w, ok := index.(*syntax.Word)
	if !ok {
		return nil, false
	}
	switch lit := w.Lit(); {
	case lit == "@" || lit == "*":
		return elems, true
	case lit != "" && digits.MatchString(lit) && !strings.ContainsAny(lit, "+-"):
		i, err := strconv.Atoi(lit)
		if err != nil {
			return nil, false
		}
		return at(i), true
	}
	return nil, false
}

// dquoteUnescape removes the backslashes a double-quoted literal keeps in
// the syntax tree: inside "…" a backslash escapes only $ ` " \ and a
// newline (`bash -c "grep \"x y\" f"` runs grep "x y" f).
func dquoteUnescape(lit string) string {
	if !strings.Contains(lit, "\\") {
		return lit
	}
	var b strings.Builder
	for i := 0; i < len(lit); i++ {
		if lit[i] == '\\' && i+1 < len(lit) && strings.IndexByte("$`\"\\\n", lit[i+1]) >= 0 {
			i++
			if lit[i] == '\n' {
				continue
			}
		}
		b.WriteByte(lit[i])
	}
	return b.String()
}

// unescape removes the backslashes an unquoted literal keeps in the syntax
// tree, as the shell does: `\X` is X and a backslash-newline is nothing.
func unescape(lit string) string {
	if !strings.Contains(lit, "\\") {
		return lit
	}
	var b strings.Builder
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		if c == '\\' && i+1 < len(lit) {
			i++
			if lit[i] == '\n' {
				continue
			}
			c = lit[i]
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Quote returns one POSIX-shell single-quoted word, the quoting
// SplitShellWords decodes. Literal newlines remain inside the quotes, so they
// are data rather than command separators (syntax.Quote refuses a newline
// under LangPOSIX, so it cannot serve here).
func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// SplitShellWords splits line into words as the shell does, removing quotes
// and backslashes, and expands nothing: a $, a backtick or a glob stays as
// written. An unquoted operator or comment is refused, never read as text.
func SplitShellWords(line string) ([]string, error) {
	words := []string{}
	end := 0
	for word, err := range syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).WordsSeq(strings.NewReader(line)) {
		if err != nil {
			return nil, fmt.Errorf("split shell words: %w", err)
		}
		if err := blankGap(line[end:word.Pos().Offset()]); err != nil {
			return nil, err
		}
		words = append(words, literal(line, word.Parts, false))
		end = int(word.End().Offset())
	}
	if err := blankGap(line[end:]); err != nil {
		return nil, err
	}
	return words, nil
}

// blankGap refuses text the word parser skipped between words: a comment.
func blankGap(gap string) error {
	if text := strings.TrimSpace(strings.ReplaceAll(gap, "\\\n", "")); text != "" {
		return fmt.Errorf("split shell words: unquoted comment %q", text)
	}
	return nil
}

// literal is a word's value with quotes and backslashes removed; any other
// part — an expansion or substitution — keeps its source text.
func literal(source string, parts []syntax.WordPart, quoted bool) string {
	var b strings.Builder
	for _, part := range parts {
		switch x := part.(type) {
		case *syntax.Lit:
			if quoted {
				b.WriteString(dquoteUnescape(x.Value))
			} else {
				b.WriteString(unescape(x.Value))
			}
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			b.WriteString(literal(source, x.Parts, true))
		default:
			b.WriteString(source[part.Pos().Offset():part.End().Offset()])
		}
	}
	return b.String()
}
