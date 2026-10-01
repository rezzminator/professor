package cmdparse

import (
	"bytes"
	"fmt"
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
	default:
		out.ok = false
	}
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
