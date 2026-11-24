// Package main is testclock: it lists every place a test or a test-support file waits on, or bounds
// by, the real clock — arch-check C36's enumerator, read with go/parser and go/ast, never a regex over
// Go code. The doors are timing-free-tests' closed list:
//
//	go run ./scripts/testclock -root DIR < list
//
// stdin lists paths relative to DIR, one a line: Go files are parsed, a fixture script (.sh, .bash,
// .txtar, .py, .js, .mjs, .cjs, or opening with #!) is read as script text, and any other file is
// data and passes. A finding prints as `{file}:{line} door {n} {spelling}`, byte-sorted:
//
//	door  package  spelling
//	 1    time     Sleep
//	 2    time     After
//	 3    time     NewTimer, NewTicker, Tick, AfterFunc
//	 4    context  WithTimeout, WithDeadline, and their Cause forms
//	 5    time     Now() then .After or .Before, or Now() as the argument of .After or .Before
//	 6    time     Since, Until, and Now() then .Sub
//	 7    clock    Real (pfm/internal/clock), except as a direct operand of == or != (an identity
//	               comparison reads no clock); a call on it, an assignment or an argument is judged
//	 8    any      .SetDeadline, .SetReadDeadline, .SetWriteDeadline, except inside a method of that
//	               name that reads no clock (a fake net.Conn forwarding the deadline it was handed)
//	 9    any      a write of .WaitDelay (an assignment or ++/--); a read compares an option
//	10    runtime  Gosched
//	11    fixture  a wait or timer in a fixture's own language, in a Go string or a fixture script:
//	               shell `sleep N` or `timeout N` (also as argv words, "sleep", "30"); Python
//	               sleep( (time.sleep, asyncio.sleep), select.select( with a timeout argument,
//	               time.time(, time.monotonic(, time.perf_counter( (the clock a self-timer
//	               compares); JavaScript setTimeout( and setInterval(
//
// A package is matched by its import path, whatever name the file imports it under. The wait door,
// internal/testwait/testwait.go, is the one file the real clock is allowed in: it is never judged.
// BROKEN STATE: an unreadable or unparsable file prints `testclock: {why}` on stderr and exits 2;
// "could not look" never reads as zero.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// door is the wait door's own file: the real clock lives there and nowhere else in the judged set.
const door = "internal/testwait/testwait.go"

// clockImport ends the import path of pfm's clock seam, whose Real is door 7: matched as a suffix,
// so a module of another name (arch-check's test fixture) is judged alike.
const clockImport = "pfm/internal/clock"

// fixtureWait is one spelling of door 11: its regexp's `spelling` group, then call, is what a
// finding prints.
type fixtureWait struct {
	re   *regexp.Regexp
	call string
}

// fixtureWaits are door 11's spellings, closed world. A shell `sleep N` or `timeout N` sits in
// command position: the start of the text or a line, after a quote, a backtick, `;`, `&`, `|`, `(`,
// or the words then, do, else and exec. The rest are calls: a Python sleep of any module, a
// select.select( with a fourth (timeout) argument, a Python clock read, a JavaScript timer.
// scripts/shtest-clock-check.tsv holds the shell check's table: a second list, since that file sits
// outside this module and its shell words (SECONDS, timeout=, --max-time) read as data in a Go string.
var fixtureWaits = []fixtureWait{
	{
		re: regexp.MustCompile(
			"(?m)(?:^|[\"'`;&|(]|\\b(?:then|do|else|exec))[ \\t]*(?P<spelling>sleep|timeout)[ \\t]+[0-9.]",
		),
	},
	{re: regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])(?P<spelling>(?:[A-Za-z_][A-Za-z0-9_]*\.)?sleep)[ \t]*\(`), call: "("},
	{
		re: regexp.MustCompile(
			`(?P<spelling>select\.select)[ \t]*\((?:[^],[]|\[[^]]*\])*(?:,(?:[^],[]|\[[^]]*\])*){3}`,
		),
		call: "(",
	},
	{re: regexp.MustCompile(`(?P<spelling>time\.(?:time|monotonic|perf_counter))[ \t]*\(`), call: "("},
	{re: regexp.MustCompile(`(?P<spelling>set(?:Timeout|Interval))[ \t]*\(`), call: "("},
}

// argvWaits are the shell waits spelled as argv words, the command then its duration ("sleep", "30").
var argvWaits = map[string]bool{"sleep": true, "timeout": true}

// argvDuration is a duration argv word: a number with an optional unit, or inf.
var argvDuration = regexp.MustCompile(`^([0-9.]+[smhd]?|inf|infinity)$`)

// timeImport is the standard library's time package; nowName its Now, and a Clock's.
const (
	timeImport = "time"
	nowName    = "Now"
)

// packageDoors maps an import path and a selected name to its door.
var packageDoors = map[string]map[string]int{
	timeImport: {
		"Sleep": 1, "After": 2,
		"NewTimer": 3, "NewTicker": 3, "Tick": 3, "AfterFunc": 3,
		"Since": 6, "Until": 6,
	},
	"context": {
		"WithTimeout": 4, "WithDeadline": 4, "WithTimeoutCause": 4, "WithDeadlineCause": 4,
	},
	"runtime":   {"Gosched": 10},
	clockImport: {"Real": 7},
}

// nowDoors are the methods banned on the result of a call of time's Now: a deadline compared to
// the real now (door 5), and time.Since respelled (door 6).
var nowDoors = map[string]int{"After": 5, "Before": 5, "Sub": 6}

// fieldDoors are the selectors banned on any receiver.
var fieldDoors = map[string]int{"SetDeadline": 8, "SetReadDeadline": 8, "SetWriteDeadline": 8, "WaitDelay": 9}

func main() {
	os.Exit(listFindings(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func listFindings(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("testclock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "the tree the listed paths are relative to")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	var findings []string
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		file := strings.TrimSpace(scanner.Text())
		if file == "" || file == door {
			continue
		}
		found, err := judge(*root, file)
		if err != nil {
			fmt.Fprintf(stderr, "testclock: %v\n", err)
			return 2
		}
		findings = append(findings, found...)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "testclock: read the list: %v\n", err)
		return 2
	}
	sort.Strings(findings)
	for _, line := range findings {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// judge lists one file's findings.
func judge(root, file string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(file, ".go") {
		if !script(file, data) {
			return nil, nil
		}
		return shellFindings(file, 1, string(data)), nil
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, data, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	names := importNames(parsed)
	var found []string
	note := func(at token.Pos, n int, spelling string) {
		found = append(found, fmt.Sprintf("%s:%d door %d %s", file, fset.Position(at).Line, n, spelling))
	}
	var forwarding []*ast.BlockStmt          // bodies of a conn's deadline methods that read no clock
	written := map[*ast.SelectorExpr]bool{}  // selectors a statement assigns to
	compared := map[*ast.SelectorExpr]bool{} // selectors that are a direct operand of == or !=
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncDecl:
			if node.Recv != nil && node.Body != nil && fieldDoors[node.Name.Name] == 8 &&
				!readsClock(node.Body, names) {
				forwarding = append(forwarding, node.Body)
			}
		case *ast.AssignStmt:
			for _, target := range node.Lhs {
				if selector, ok := target.(*ast.SelectorExpr); ok {
					written[selector] = true
				}
			}
		case *ast.BinaryExpr:
			if node.Op == token.EQL || node.Op == token.NEQ {
				for _, operand := range []ast.Expr{node.X, node.Y} {
					if selector, ok := ast.Unparen(operand).(*ast.SelectorExpr); ok {
						compared[selector] = true
					}
				}
			}
		case *ast.IncDecStmt:
			if selector, ok := node.X.(*ast.SelectorExpr); ok {
				written[selector] = true
			}
		case *ast.SelectorExpr:
			if n, ok := fieldDoors[node.Sel.Name]; ok {
				switch {
				case n == 8 && insideBody(node, forwarding):
					// A fake conn's deadline method forwarding the deadline it was handed (ruling 36).
				case n == 9 && !written[node]:
					// A read of WaitDelay compares a production option; only a write arms one (ruling 23).
				default:
					note(node.Sel.Pos(), n, "."+node.Sel.Name)
				}
				return true
			}
			if pkg, ok := node.X.(*ast.Ident); ok {
				if n, ok := packageDoors[names[pkg.Name]][node.Sel.Name]; ok && (n != 7 || !compared[node]) {
					// An identity comparison with clock.Real reads no clock and cannot wait (ruling 72).
					note(node.Pos(), n, path.Base(names[pkg.Name])+"."+node.Sel.Name)
				}
			}
			if n, ok := nowDoors[node.Sel.Name]; ok {
				if timeNow(node.X, names) {
					note(node.Pos(), n, timeImport+"."+nowName+"()."+node.Sel.Name)
				}
			}
		case *ast.CallExpr:
			found = append(found, argvFindings(file, fset, node.Args)...)
			method, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || nowDoors[method.Sel.Name] != 5 || timeNow(method.X, names) {
				break
			}
			for _, arg := range node.Args {
				if timeNow(arg, names) {
					// A deadline compared to the real now from the argument side.
					note(method.Sel.Pos(), 5, "."+method.Sel.Name+"("+timeImport+"."+nowName+"())")
				}
			}
		case *ast.CompositeLit:
			found = append(found, argvFindings(file, fset, node.Elts)...)
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				if text, err := strconv.Unquote(node.Value); err == nil {
					found = append(found, shellFindings(file, fset.Position(node.Pos()).Line, text)...)
				}
			}
		}
		return true
	})
	return found, nil
}

// timeNow reports whether expr is a call of the standard library time package's Now.
func timeNow(expr ast.Expr, names map[string]string) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	now, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || now.Sel.Name != nowName {
		return false
	}
	pkg, ok := now.X.(*ast.Ident)
	return ok && names[pkg.Name] == timeImport
}

// insideBody reports whether node sits inside one of bodies.
func insideBody(node ast.Node, bodies []*ast.BlockStmt) bool {
	for _, body := range bodies {
		if node.Pos() >= body.Pos() && node.End() <= body.End() {
			return true
		}
	}
	return false
}

// readsClock reports whether body reads a clock: any selector of package time that tells or waits on
// the time (Now, Since, Until, and every door of packageDoors), any name of pfm's clock package, or
// a call of a method named Now on anything (a Clock's).
func readsClock(body *ast.BlockStmt, names map[string]string) bool {
	reads := false
	ast.Inspect(body, func(node ast.Node) bool {
		if reads {
			return false
		}
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := node.X.(*ast.Ident); ok {
				switch importPath := names[pkg.Name]; {
				case importPath == clockImport:
					reads = true
				case importPath == timeImport && (node.Sel.Name == nowName || packageDoors[timeImport][node.Sel.Name] != 0):
					reads = true
				}
			}
		case *ast.CallExpr:
			if method, ok := node.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == nowName {
				reads = true
			}
		}
		return !reads
	})
	return reads
}

// script reports whether a non-Go file runs as a fixture (a .sh, .bash, .txtar, .py, .js, .mjs or
// .cjs file, or one opening with #!), and not as data a test reads.
func script(file string, data []byte) bool {
	switch filepath.Ext(file) {
	case ".sh", ".bash", ".txtar", ".py", ".js", ".mjs", ".cjs":
		return true
	}
	return strings.HasPrefix(string(data), "#!")
}

// importNames maps each name a file refers to an imported package by to that package's path.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if strings.HasSuffix(importPath, "/"+clockImport) || importPath == clockImport {
			importPath = clockImport
		}
		names[name] = importPath
	}
	return names
}

// shellFindings lists every door 11 spelling in text, whose first line is line `first` of file.
func shellFindings(file string, first int, text string) []string {
	var found []string
	for _, wait := range fixtureWaits {
		group := 2 * wait.re.SubexpIndex("spelling")
		for _, at := range wait.re.FindAllStringSubmatchIndex(text, -1) {
			line := first + strings.Count(text[:at[group]], "\n")
			found = append(found, fmt.Sprintf("%s:%d door 11 %s%s", file, line, text[at[group]:at[group+1]], wait.call))
		}
	}
	return found
}

// argvFindings lists a shell wait spelled as two argv words of a call or a literal list: the string
// "sleep" or "timeout", then a duration string.
func argvFindings(file string, fset *token.FileSet, items []ast.Expr) []string {
	var found []string
	for i := 0; i+1 < len(items); i++ {
		word, duration := stringValue(items[i]), stringValue(items[i+1])
		if argvWaits[word] && argvDuration.MatchString(duration) {
			found = append(found, fmt.Sprintf("%s:%d door 11 %s", file, fset.Position(items[i].Pos()).Line, word))
		}
	}
	return found
}

// stringValue is the value of a string literal, or "" for any other expression.
func stringValue(expr ast.Expr) string {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	return value
}
