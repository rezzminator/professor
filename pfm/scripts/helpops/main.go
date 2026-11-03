// Package main derives the public operation census from dispatchers and registries.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

func main() { os.Exit(runHelpOps(os.Args[1:], os.Stderr)) }

func runHelpOps(args []string, stderr io.Writer) int {
	flags := cli.NewFlagSet("helpops", "usage: helpops [-root PFM_DIR] [-check]", stderr)
	root := flags.String("root", ".", "pfm source directory")
	check := flags.Bool("check", false, "refuse a stale generated census")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if err := generateHelpOps(*root, *check); err != nil {
		fmt.Fprintln(stderr, "help --ops: DERIVE-FAILED:", err)
		return 1
	}
	return 0
}

func generateHelpOps(root string, check bool) error {
	const dispatchDirectory = "cmd/pfm"
	// Dispatch locations identify code, never a second list of operation names.
	sites := []struct {
		directory, symbol, prefix string
		argument                  int
	}{
		{dispatchDirectory, "topLevelSubcommands", "pfm", 0},
		{dispatchDirectory, "runChatWithRuntime", "pfm chat", 0},
		{dispatchDirectory, "runConfig", "pfm config", 0},
		{dispatchDirectory, "runMCP", "pfm mcp", 0},
		{dispatchDirectory, "runMCP", "pfm mcp <server>", 1},
		{dispatchDirectory, "runHeadless", "pfm headless", 0},
		{"internal/schedule", "RunScheduleCommand", "pfm chat schedule", 0},
		{"internal/themecmd", "RunVerb", "pfm theme", 0},
		{"internal/update", "Run", "pfm update", 0},
		{"internal/transcript/digest", "verbChoices", "pfm transcript", 0},
	}
	expressions := map[string]string{
		"pfmengine.MustLookup(pfmengine.Claude).LongName": pfmengine.MustLookup(pfmengine.Claude).LongName,
	}
	operations := map[string]bool{}
	for _, site := range sites {
		names, err := filepath.Glob(filepath.Join(root, site.directory, "*.go"))
		if err != nil {
			return err
		}
		sources := map[string]string{}
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			content, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			sources[name] = string(content)
		}
		verbs, err := dispatchVerbs(sources, site.symbol, site.argument, expressions)
		if err != nil {
			return fmt.Errorf("%s: %w", site.directory, err)
		}
		for _, verb := range verbs {
			if strings.Contains(site.prefix, "<server>") {
				for _, server := range config.RegisteredMCPServers() {
					operations[strings.Replace(site.prefix, "<server>", server, 1)+" "+verb] = true
				}
			} else {
				operations[site.prefix+" "+verb] = true
			}
		}
	}
	for _, name := range mcpserv.ToolNames() {
		operations["mcp__"+config.MCPRegistrationName(config.MCPServerChat)+"__"+name] = true
	}
	for _, hook := range claudelaunch.HookTemplates("") {
		operations["hook.claude."+hook.Event+"."+hook.Name] = true
	}
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return writeHelpOps(
		filepath.Join(root, "internal", "cli", "help_ops.txt"),
		[]byte(strings.Join(names, "\n")+"\n"),
		check,
	)
}

func writeHelpOps(path string, content []byte, check bool) error {
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, content) {
			return fmt.Errorf("generated census is stale; run go generate ./internal/cli")
		}
		return nil
	}
	return atomicfile.Write(path, content, 0o644)
}

func formatNode(w io.Writer, node ast.Node) error { return format.Node(w, token.NewFileSet(), node) }

// dispatchVerbs reads case labels from a dispatch function, or values from a parser's verb registry.
func dispatchVerbs(
	sources map[string]string,
	symbol string,
	argument int,
	expressions map[string]string,
) ([]string, error) {
	files := make([]*ast.File, 0, len(sources))
	values := map[string]ast.Expr{}
	for name, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		files = append(files, file)
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range group.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					if index < len(value.Values) {
						values[name.Name] = value.Values[index]
					}
				}
			}
		}
	}
	var resolve func(ast.Expr) (string, error)
	resolve = func(expr ast.Expr) (string, error) {
		switch value := expr.(type) {
		case *ast.BasicLit:
			if value.Kind == token.STRING {
				return strconv.Unquote(value.Value)
			}
		case *ast.Ident:
			if ref, ok := values[value.Name]; ok {
				return resolve(ref)
			}
		}
		var rendered strings.Builder
		if err := formatNode(&rendered, expr); err != nil {
			return "", err
		}
		if value, ok := expressions[rendered.String()]; ok {
			return value, nil
		}
		return "", fmt.Errorf("unresolved dispatch value %s", rendered.String())
	}
	var found ast.Node
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == symbol {
				found = fn.Body
			}
		}
	}
	if found == nil {
		if list, ok := values[symbol].(*ast.CompositeLit); ok {
			found = list
		}
	}
	if found == nil {
		return nil, fmt.Errorf("dispatch symbol %s not found", symbol)
	}
	var verbs []string
	var failure error
	add := func(expr ast.Expr) {
		verb, err := resolve(expr)
		if err != nil {
			failure = err
			return
		}
		if verb != "help" && verb != "internal" && !strings.HasPrefix(verb, "-") {
			verbs = append(verbs, verb)
		}
	}
	if list, ok := found.(*ast.CompositeLit); ok {
		for _, expr := range list.Elts {
			add(expr)
		}
	} else {
		ast.Inspect(found, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CaseClause:
				if argument == 0 {
					for _, expr := range value.List {
						add(expr)
					}
				}
			case *ast.BinaryExpr:
				if value.Op == token.EQL || value.Op == token.NEQ {
					if argAt(value.X, argument) {
						add(value.Y)
					} else if argAt(value.Y, argument) {
						add(value.X)
					}
				}
			}
			return failure == nil
		})
	}
	if failure != nil {
		return nil, failure
	}
	if len(verbs) == 0 {
		return nil, fmt.Errorf("dispatch symbol %s has no public verbs", symbol)
	}
	return verbs, nil
}

func argAt(expr ast.Expr, argument int) bool {
	index, ok := expr.(*ast.IndexExpr)
	if !ok {
		return false
	}
	name, ok := index.X.(*ast.Ident)
	if !ok || name.Name != "args" {
		return false
	}
	literal, ok := index.Index.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == strconv.Itoa(argument)
}
