// go-inspect reads syntax without executing product code or shell commands.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type Record struct {
	File     string            `json:"file"`
	Line     int               `json:"line"`
	Function string            `json:"function"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Text     string            `json:"text"`
	Args     []string          `json:"args,omitempty"`
	Bindings map[string]string `json:"bindings,omitempty"`
	Labels   []string          `json:"labels,omitempty"`
	Beat     string            `json:"beat,omitempty"`
}

func main() {
	records := []Record{}
	for _, path := range os.Args[1:] {
		if path == "--" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		switch filepath.Ext(path) {
		case ".go":
			records, err = inspectGo(path, data, records)
		case ".sh":
			records, err = inspectShell(path, data, records)
		default:
			err = fmt.Errorf("unsupported source %s", path)
		}
		if err != nil {
			fail(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(records); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }

func inspectGo(path string, data []byte, records []Record) ([]Record, error) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, path, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	text := func(n ast.Node) string {
		var out bytes.Buffer
		if err := format.Node(&out, fs, n); err != nil {
			fail(err)
		}
		return out.String()
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bindingsFor := func(node ast.Node) map[string]string {
			bindings := map[string]string{}
			seen := map[*ast.Object]bool{}
			var bind func(ast.Node)
			bind = func(subject ast.Node) {
				ast.Inspect(subject, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok || id.Obj == nil || seen[id.Obj] {
						return true
					}
					seen[id.Obj] = true
					var value ast.Expr
					switch declaration := id.Obj.Decl.(type) {
					case *ast.AssignStmt:
						for i, lhs := range declaration.Lhs {
							if variable, ok := lhs.(*ast.Ident); ok && variable.Obj == id.Obj && i < len(declaration.Rhs) {
								value = declaration.Rhs[i]
							}
						}
					case *ast.ValueSpec:
						for i, variable := range declaration.Names {
							if variable.Obj == id.Obj && i < len(declaration.Values) {
								value = declaration.Values[i]
							}
						}
					}
					if value != nil {
						bindings[id.Name] = text(value)
						bind(value)
					}
					return true
				})
			}
			bind(node)
			return bindings
		}
		type subtest struct {
			name       string
			start, end token.Pos
		}
		subtests := []subtest{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 || !strings.HasSuffix(text(call.Fun), ".Run") {
				return true
			}
			name, ok := call.Args[0].(*ast.BasicLit)
			if !ok || name.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(name.Value)
			if err != nil {
				return true
			}
			body, ok := call.Args[1].(*ast.FuncLit)
			if ok {
				subtests = append(subtests, subtest{value, body.Pos(), body.End()})
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				return true
			}
			r := Record{File: path, Line: fs.Position(n.Pos()).Line, Function: fn.Name.Name}
			for _, test := range subtests {
				if n.Pos() >= test.start && n.End() <= test.end {
					r.Labels = append(r.Labels, test.name)
				}
			}
			switch n := n.(type) {
			case *ast.CallExpr:
				r.Kind, r.Name, r.Text = "call", text(n.Fun), text(n)
				if strings.HasSuffix(path, "_test.go") && (strings.HasSuffix(r.Name, ".Equal") || strings.HasSuffix(r.Name, ".EqualValues") || strings.HasSuffix(r.Name, ".Diff")) {
					r.Bindings = bindingsFor(n)
				}
				for _, a := range n.Args {
					r.Args = append(r.Args, text(a))
				}
				records = append(records, r)
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					r.Kind, r.Name, r.Text, r.Args = "comparison", n.Op.String(), text(n), []string{text(n.X), text(n.Y)}
					if strings.HasSuffix(path, "_test.go") {
						r.Bindings = bindingsFor(n)
					}
					records = append(records, r)
				}
			}
			return true
		})
	}
	return records, nil
}

var internalEntry = regexp.MustCompile(`(^|[^[:alnum:]_])(pfm|\$\{?PFM[_A-Z]*(\})?|\$\{PFM[_A-Z]*:-pfm\})["']?[[:space:]]+["']?(internal|__|hook)([^[:alnum:]_]|$)|PFM_INTERNAL|herdr-native\.c`)

func inspectShell(path string, data []byte, records []Record) ([]Record, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(bytes.NewReader(data), path)
	if err != nil {
		return nil, err
	}
	print := func(n syntax.Node) string {
		var out bytes.Buffer
		if err := syntax.NewPrinter(syntax.SingleLine(true)).Print(&out, n); err != nil {
			fail(err)
		}
		return out.String()
	}
	currentBeat := ""
	lines := strings.Split(string(data), "\n")
	bodies := map[*syntax.CallExpr]string{}
	syntax.Walk(file, func(node syntax.Node) bool {
		clause, ok := node.(*syntax.IfClause)
		if !ok {
			return true
		}
		for _, statement := range clause.Cond {
			syntax.Walk(statement, func(node syntax.Node) bool {
				call, ok := node.(*syntax.CallExpr)
				if ok && len(call.Args) > 1 && strings.Trim(print(call.Args[0]), `"'`) == "beat" {
					bodies[call] = print(clause)
				}
				return true
			})
		}
		return true
	})
	var walk func(syntax.Node, bool, string)
	walk = func(node syntax.Node, allowed bool, function string) {
		syntax.Walk(node, func(n syntax.Node) bool {
			if n == nil {
				return true
			}
			switch n := n.(type) {
			case *syntax.FuncDecl:
				allow := n.Name.Value == "need" || strings.HasPrefix(n.Name.Value, "need_") || strings.HasPrefix(n.Name.Value, "observe_")
				walk(n.Body, allowed || allow, n.Name.Value)
				return false
			case *syntax.CallExpr:
				command := ""
				if len(n.Args) > 0 {
					command = strings.Trim(print(n.Args[0]), `"'`)
				}
				if command == "beat" && len(n.Args) > 1 {
					currentBeat = strings.Trim(print(n.Args[1]), `"'`)
					records = append(records, Record{File: path, Line: int(n.Pos().Line()), Function: function, Kind: "beat", Name: currentBeat, Text: bodies[n]})
				}
				if command == "assert" || command == "golden" || command == "expect-log" || command == "fail" {
					line := int(n.Pos().Line())
					record := Record{File: path, Line: line, Function: function, Kind: "assertion", Name: command, Text: strings.TrimSpace(lines[line-1]), Beat: currentBeat}
					for _, arg := range n.Args[1:] {
						record.Args = append(record.Args, print(arg))
					}
					records = append(records, record)
				}
				allow := allowed || command == "need" || command == "observe" || command == "assert" || command == "golden" || command == "expect-log"
				statement := print(n)
				if internalEntry.MatchString(statement) && !allow {
					records = append(records, Record{File: path, Line: int(n.Pos().Line()), Function: function, Kind: "internal-action", Name: command, Text: statement})
				}
				// A quoted action string is already checked above; nested substitutions retain the enclosing observation context.
				for _, word := range n.Args {
					syntax.Walk(word, func(child syntax.Node) bool {
						if child == nil {
							return true
						}
						if sub, ok := child.(*syntax.CmdSubst); ok {
							for _, stmt := range sub.Stmts {
								walk(stmt, allow, function)
							}
							return false
						}
						return true
					})
				}
				return false
			}
			return true
		})
	}
	walk(file, false, "")
	return records, nil
}
