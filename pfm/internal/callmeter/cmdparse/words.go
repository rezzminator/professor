package cmdparse

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// word is one shell word evaluated against the literal variables known so
// far: ok when every part resolved, glob when an unquoted part carries an
// unescaped glob metacharacter. unmatched runs beside vals: a value built
// from a variable's unmatched glob.
type word struct {
	vals      []string
	unmatched []bool
	ok        bool
	glob      bool
}

const maxExpansions = 64

func (p *callParser) eval(w *syntax.Word) word {
	out := word{vals: []string{""}, unmatched: []bool{false}, ok: true}
	for i, part := range w.Parts {
		if lit, isLit := part.(*syntax.Lit); isLit && i == 0 && strings.HasPrefix(lit.Value, "~") {
			if !p.tilde(lit.Value, &out) {
				return word{}
			}
			continue
		}
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
			out.appendAll([]string{dquoteUnescape(x.Value)}, nil)
			return
		}
		value, glob := unescape(x.Value)
		if glob {
			out.glob = true
		}
		out.appendAll([]string{value}, nil)
	case *syntax.SglQuoted:
		out.appendAll([]string{x.Value}, nil)
	case *syntax.DblQuoted:
		for _, inner := range x.Parts {
			p.evalPart(inner, out, true)
		}
	case *syntax.ParamExp:
		vals, unmatched, ok := p.param(x)
		if !ok {
			out.ok = false
			return
		}
		out.appendAll(vals, unmatched)
	default:
		out.ok = false
	}
}

// tilde expands a word's leading unquoted literal that starts with `~`: `~`
// and `~/…` are the call's home. `~user`, `~+`, `~-`, or no home known leave
// the word unknown, so it attributes nothing rather than a path under cwd.
func (p *callParser) tilde(lit string, out *word) bool {
	prefix, rest, _ := strings.Cut(lit, "/")
	if prefix != "~" || p.call.Home == "" {
		return false
	}
	if strings.Contains(lit, "/") {
		rest = "/" + rest
	}
	value, glob := unescape(rest)
	out.glob = out.glob || glob
	out.appendAll([]string{p.call.Home + value}, nil)
	return true
}

// appendAll appends every suffix to every value so far; unmatched marks
// the suffixes that come from an unmatched glob (nil: none), and a value
// built from one stays marked.
func (w *word) appendAll(suffixes []string, unmatched []bool) {
	if !w.ok {
		return
	}
	next := make([]string, 0, len(w.vals)*len(suffixes))
	nextUnmatched := make([]bool, 0, cap(next))
	for i, v := range w.vals {
		for j, s := range suffixes {
			next = append(next, v+s)
			nextUnmatched = append(nextUnmatched, w.unmatched[i] || (j < len(unmatched) && unmatched[j]))
		}
	}
	if len(next) > maxExpansions {
		w.ok = false
		return
	}
	w.vals, w.unmatched = next, nextUnmatched
}

// param expands a parameter to its values and, beside them, their
// unmatched-glob marks (nil: none).
func (p *callParser) param(x *syntax.ParamExp) ([]string, []bool, bool) {
	if x.Param == nil || x.Excl || x.Length || x.Width || x.Slice != nil ||
		x.Repl != nil || x.Exp != nil || x.Names != 0 {
		return nil, nil, false
	}
	marks := p.unmatched[x.Param.Value]
	if elems, isArray := p.arrays[x.Param.Value]; isArray {
		vals, ok := arrayElems(elems, x.Index)
		if !ok {
			return nil, nil, false
		}
		selected, _ := arrayElems(marks, x.Index) // same index: ok as above
		return vals, selected, true
	}
	if x.Index != nil {
		return nil, nil, false
	}
	vals, ok := p.vars[x.Param.Value]
	return vals, marks, ok
}

// text renders a word as the program receives it where that is known, and
// as its source where it is not: a -c argument or a heredoc body keeps its
// unexpanded `$X` rather than being dropped.
func (p *callParser) text(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	var b strings.Builder
	var render func(parts []syntax.WordPart, quoted bool)
	render = func(parts []syntax.WordPart, quoted bool) {
		for _, part := range parts {
			switch x := part.(type) {
			case *syntax.Lit:
				if quoted {
					b.WriteString(dquoteUnescape(x.Value))
					continue
				}
				b.WriteString(x.Value)
			case *syntax.SglQuoted:
				b.WriteString(x.Value)
			case *syntax.DblQuoted:
				render(x.Parts, true)
			case *syntax.ParamExp:
				if vals, _, ok := p.param(x); ok && len(vals) == 1 {
					b.WriteString(vals[0])
					continue
				}
				b.WriteString(p.source(x))
			default:
				b.WriteString(p.source(x))
			}
		}
	}
	render(w.Parts, false)
	return b.String()
}

func (p *callParser) source(node syntax.Node) string {
	var buf bytes.Buffer
	if err := p.printer.Print(&buf, node); err != nil {
		return fmt.Sprintf("<unprintable: %v>", err)
	}
	return buf.String()
}

// arg is one argument as the program sees it; a word that expands to
// several values (a glob, a loop variable) is several args. resolved is
// false when the value is not known, and text then holds the source.
// unmatched marks a glob that matched nothing, left as written.
type arg struct {
	text      string
	resolved  bool
	word      *syntax.Word
	unmatched bool
}

func (p *callParser) args(w *syntax.Word) []arg {
	var out []arg
	for _, bw := range braces(w) {
		ev := p.eval(bw)
		if !ev.ok {
			return []arg{{text: p.source(w), word: w}}
		}
		for i, v := range ev.vals {
			matches, unmatched := p.glob(v, ev.glob)
			for _, m := range matches {
				out = append(out, arg{text: m, resolved: true, word: w, unmatched: unmatched || ev.unmatched[i]})
			}
		}
	}
	return out
}

// braces brace-expands an argument word as bash does before any other
// expansion: `{a,b}.go` is a.go and b.go. The word itself is left untouched.
// A sequence (`{1..9}`) or a product over maxExpansions words stays one
// literal word, which then names a file only if that exact file exists.
func braces(w *syntax.Word) []*syntax.Word {
	split := &syntax.Word{Parts: slices.Clone(w.Parts)}
	if !syntax.SplitBraces(split) {
		return []*syntax.Word{w}
	}
	product := 1
	if !braceBound(split, &product) {
		return []*syntax.Word{w}
	}
	return expand.Braces(split)
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

// assignArray keeps NAME=(a b c) when every element is literal, globs
// expanded as for any argument; one element the parse cannot know, or an
// explicit [i]=v index, drops the array.
func (p *callParser) assignArray(name string, arr *syntax.ArrayExpr) {
	delete(p.vars, name)
	delete(p.unmatched, name)
	delete(p.arrays, name)
	var elems []string
	var marks []bool
	for _, e := range arr.Elems {
		if e.Index != nil || e.Value == nil {
			return
		}
		for _, a := range p.args(e.Value) {
			if !a.resolved {
				return
			}
			elems = append(elems, a.text)
			marks = append(marks, a.unmatched)
		}
	}
	p.arrays[name], p.unmatched[name] = elems, marks
}

// arrayElems expands an array reference: [@] and [*] are every element, a
// literal [N] is that element, no index is the first. Past the end is the
// zero value, as bash expands an unset element to "".
func arrayElems[T any](elems []T, index syntax.ArithmExpr) ([]T, bool) {
	at := func(i int) []T {
		if i < len(elems) {
			return []T{elems[i]}
		}
		var zero T
		return []T{zero}
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
// glob reports an unescaped glob metacharacter; an escaped one is literal.
func unescape(lit string) (value string, glob bool) {
	if !strings.Contains(lit, "\\") {
		return lit, strings.ContainsAny(lit, "*?[")
	}
	var b strings.Builder
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		switch {
		case c == '\\' && i+1 < len(lit):
			i++
			if lit[i] == '\n' {
				continue
			}
			c = lit[i]
		case strings.IndexByte("*?[", c) >= 0:
			glob = true
		}
		b.WriteByte(c)
	}
	return b.String(), glob
}
