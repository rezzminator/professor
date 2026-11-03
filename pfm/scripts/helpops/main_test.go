package main

import (
	"bytes"
	"errors"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestDispatchVerbs(t *testing.T) {
	for _, row := range []struct {
		name, source, symbol string
		want                 []string
		wantError            string
		argument             int
		expressions          map[string]string
	}{
		{
			name: "switch constants and aliases",
			source: `package fixture
const create = "new"
func dispatch(args []string) {
 switch args[0] { case create:; case "show", "list":; case "help", "--help", "internal": }
}`,
			symbol: "dispatch", want: []string{"new", "show", "list"},
		},
		{
			name: "if dispatcher",
			source: `package fixture
func dispatch(args []string) {
 if args[0] == "serve" {} else if "ls" == args[0] {}
 if args[1] == "hidden" {}
}`,
			symbol: "dispatch", want: []string{"serve", "ls"},
		},
		{
			name: "parser registry",
			source: `package fixture
var choices = []string{"show", "locate", "agents"}`,
			symbol: "choices", want: []string{"show", "locate", "agents"},
		},
		{
			name: "second argument refusal",
			source: `package fixture
func dispatch(args []string) {
 if args[1] != "enable" && args[1] != "disable" {}
}`,
			symbol: "dispatch", argument: 1, want: []string{"enable", "disable"},
		},
		{
			name: "registered expression",
			source: `package fixture
func dispatch(args []string) { switch args[0] { case engine.Lookup().Name: } }`,
			symbol: "dispatch", want: []string{"fixture-engine"},
			expressions: map[string]string{"engine.Lookup().Name": "fixture-engine"},
		},
		{name: "absent source", symbol: "dispatch", wantError: "dispatch symbol dispatch not found"},
		{name: "empty source", source: "package fixture", symbol: "dispatch", wantError: "dispatch symbol dispatch not found"},
		{name: "malformed source", source: "package", symbol: "dispatch", wantError: "parse fixture.go:"},
		{
			name: "no verbs", source: `package fixture; func dispatch() {}`,
			symbol: "dispatch", wantError: "dispatch symbol dispatch has no public verbs",
		},
		{
			name:   "unresolved verb",
			source: `package fixture; func dispatch(args []string) { switch args[0] { case unknown: } }`,
			symbol: "dispatch", wantError: "unresolved dispatch value unknown",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			sources := map[string]string{}
			if row.source != "" {
				sources["fixture.go"] = row.source
			}
			got, err := dispatchVerbs(sources, row.symbol, row.argument, row.expressions)
			if row.name == "malformed source" {
				var failures scanner.ErrorList
				if !errors.As(err, &failures) || len(failures) == 0 {
					t.Fatalf("dispatchVerbs error = %v, want Go syntax error at fixture.go:1:8", err)
				}
				want := &scanner.Error{
					Pos: token.Position{Filename: "fixture.go", Offset: 7, Line: 1, Column: 8},
					Msg: "expected 'IDENT', found 'EOF'",
				}
				if !reflect.DeepEqual(failures[0], want) {
					t.Fatalf("dispatchVerbs syntax error = %#v, want %#v", failures[0], want)
				}
				return
			}
			if row.wantError != "" {
				if err == nil {
					t.Fatalf("dispatchVerbs error = nil, want %q", row.wantError)
				}
				if actual := err.Error(); actual != row.wantError {
					t.Fatalf("dispatchVerbs error = %q, want %q", actual, row.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, row.want) {
				t.Fatalf("dispatchVerbs = %#v, want %#v", got, row.want)
			}
		})
	}
}

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func TestWriteHelpOps(t *testing.T) {
	for _, row := range []struct {
		name, initial string
		check         bool
		wantError     string
	}{
		{name: "write", initial: "old\n"},
		{name: "current", initial: "new\n", check: true},
		{name: "stale", initial: "old\n", check: true, wantError: "generated census is stale; run go generate ./internal/cli"},
	} {
		t.Run(row.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "census.txt")
			if err := os.WriteFile(path, []byte(row.initial), 0o644); err != nil {
				t.Fatal(err)
			}
			err := writeHelpOps(path, []byte("new\n"), row.check)
			if row.wantError != "" {
				if err == nil || err.Error() != row.wantError {
					t.Fatalf("writeHelpOps error = %v, want %q", err, row.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "new\n"
			if row.wantError != "" {
				want = row.initial
			}
			if string(actual) != want {
				t.Fatalf("census at %s = %q, want %q", path, actual, want)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	if err := writeHelpOps(missing, []byte("new\n"), true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check missing %s error = %v, want os.ErrNotExist", missing, err)
	}
}

func TestRunHelpOpsReportsDerivationFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := runHelpOps([]string{"-root", t.TempDir(), "-check"}, &stderr); code != 1 {
		t.Fatalf("missing dispatch source exit = %d, want 1", code)
	}
	want := "help --ops: DERIVE-FAILED: cmd/pfm: dispatch symbol topLevelSubcommands not found\n"
	if actual := stderr.String(); actual != want {
		t.Fatalf("missing dispatch source stderr = %q, want %q", actual, want)
	}
}
