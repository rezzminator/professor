package paths

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail/sourcesweep"
)

// A filesystem location is a literal only here, in its door. Production code
// elsewhere names none — a temp, proc or socket directory, a $HOME or $TMPDIR
// spelled in place — and resolves no home or temp directory of its own; no Go
// file, production or test, asks the OS for a temp directory itself. Readers
// that parse a location's spelling may name only the token they consume. An
// entry that goes stale fails with the same sweep.
type locationAllowance struct {
	reason string
	tokens []string
}

var locationTokenReaders = map[string]locationAllowance{
	"internal/config/config.go": {
		"parses the $HOME/ prefix a configured path may carry; expansion joins the home it is handed",
		[]string{"$HOME"},
	},
	"internal/hookentry/store_guard.go": {
		"parses the shell spellings of HOME in a hook's store path; expansion joins the home it is handed",
		[]string{"${HOME}", "$HOME"},
	},
	"internal/testjail/enginesync.go": {
		"shell text of the fake engine-sync binary; its own shell expands HOME from the jail's environment",
		[]string{"$HOME"},
	},
	"internal/testjail/executable.go": {
		"shell text of the fake engine binaries; their own shell expands HOME from the jail's environment",
		[]string{"$HOME"},
	},
}

// locationPatterns find a location spelled in a string literal. A /tmp or
// /proc counts only as a path's own segment, never inside a longer word.
var locationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`/private/tmp`),
	regexp.MustCompile(`/var/folders`),
	regexp.MustCompile(`(?:^|[^\w.~-])(/tmp)(?:/|$|[^\w-])`),
	regexp.MustCompile(`(?:^|[^\w.~-])(/proc)(?:/|$|[^\w-])`),
	regexp.MustCompile(`\$\{(?:HOME|TMPDIR|TMUX_TMPDIR)\}|\$(?:HOME|TMPDIR|TMUX_TMPDIR)\b`),
}

// socketDirNames are the names the door gives a socket directory; a bare one
// elsewhere is that directory built in place.
var socketDirNames = []string{"tmux-", "cc-sid"}

// homeOrTempEnv are the variables only the door reads.
var homeOrTempEnv = []string{"HOME", EnvTmpDir, EnvTmuxTmpDir}

func locationTokens(value string) []string {
	var tokens []string
	if slices.Contains(socketDirNames, value) {
		tokens = append(tokens, value)
	}
	for _, pattern := range locationPatterns {
		for _, match := range pattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 && match[1] != "" {
				tokens = append(tokens, match[1])
			} else {
				tokens = append(tokens, match[0])
			}
		}
	}
	return tokens
}

// osCall names the os function a call expression invokes, "" for any other.
func osCall(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || receiver.Name != "os" {
		return ""
	}
	return selector.Sel.Name
}

func stringArgument(call *ast.CallExpr, index int) (string, bool) {
	if len(call.Args) <= index {
		return "", false
	}
	literal, ok := call.Args[index].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// locationCall reports a call that resolves a temp or home directory outside
// the door; production reports the env and home reads too.
func locationCall(call *ast.CallExpr, production bool) string {
	name := osCall(call)
	switch name {
	case "TempDir":
		return "os.TempDir()"
	case "MkdirTemp", "CreateTemp":
		if dir, ok := stringArgument(call, 0); ok && dir == "" {
			return fmt.Sprintf("os.%s(\"\", …)", name)
		}
	case "UserHomeDir":
		if production {
			return "os.UserHomeDir()"
		}
	case "Getenv", "LookupEnv":
		if key, ok := stringArgument(call, 0); ok && production && slices.Contains(homeOrTempEnv, key) {
			return fmt.Sprintf("os.%s(%q)", name, key)
		}
	}
	return ""
}

func TestNoLocationLiteralOutsidePaths(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate location sweep source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	// The walk only lists the files; parsing them is the cost (about 2,800 files),
	// so it runs on every core, each file's findings kept in its own slot.
	type sweepFile struct {
		rel, path  string
		production bool
	}
	var files []sweepFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "internal/paths" || entry.Name() == "testdata" || sourcesweep.SkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") {
			files = append(files, sweepFile{rel: rel, path: path, production: !strings.HasSuffix(rel, "_test.go")})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("location literal sweep of %s failed — a failure to look, not a clean result: %v", root, err)
	}
	type sweepResult struct {
		hits, allowed []string
		err           error
	}
	results := make([]sweepResult, len(files))
	next := make(chan int)
	var workers sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range next {
				f, result := files[i], &results[i]
				positions := token.NewFileSet()
				file, err := parser.ParseFile(positions, f.path, nil, parser.SkipObjectResolution)
				if err != nil {
					result.err = err
					continue
				}
				ast.Inspect(file, func(node ast.Node) bool {
					switch node := node.(type) {
					case *ast.CallExpr:
						if call := locationCall(node, f.production); call != "" {
							result.hits = append(
								result.hits,
								fmt.Sprintf("%s:%d: %s", f.rel, positions.Position(node.Pos()).Line, call),
							)
						}
					case *ast.BasicLit:
						if !f.production || node.Kind != token.STRING {
							return true
						}
						line := positions.Position(node.Pos()).Line
						value, err := strconv.Unquote(node.Value)
						if err != nil {
							result.hits = append(result.hits, fmt.Sprintf("%s:%d: unquote: %v", f.rel, line, err))
							return true
						}
						for _, found := range locationTokens(value) {
							if allowance, allowed := locationTokenReaders[f.rel]; allowed &&
								strings.TrimSpace(allowance.reason) != "" && slices.Contains(allowance.tokens, found) {
								result.allowed = append(result.allowed, found)
								continue
							}
							result.hits = append(result.hits, fmt.Sprintf("%s:%d: %q in %q", f.rel, line, found, value))
						}
					}
					return true
				})
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	workers.Wait()
	seen := map[string]map[string]bool{}
	var hits []string
	for i, result := range results {
		if result.err != nil {
			t.Fatalf(
				"location literal sweep of %s failed — a failure to look, not a clean result: parse %s: %v",
				root,
				files[i].rel,
				result.err,
			)
		}
		hits = append(hits, result.hits...)
		for _, found := range result.allowed {
			if seen[files[i].rel] == nil {
				seen[files[i].rel] = map[string]bool{}
			}
			seen[files[i].rel][found] = true
		}
	}
	for file, allowance := range locationTokenReaders {
		for _, found := range allowance.tokens {
			if strings.TrimSpace(allowance.reason) == "" || !seen[file][found] {
				hits = append(hits, fmt.Sprintf("stale allow-list entry %s: %q (%s)", file, found, allowance.reason))
			}
		}
	}
	if len(hits) != 0 {
		sort.Strings(hits)
		for _, hit := range hits {
			t.Error(hit)
		}
		t.Fatalf("location literal sweep of %s: %d findings; resolve each through internal/paths", root, len(hits))
	}
}
