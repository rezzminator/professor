package claudelaunch

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPackageDocNamesWhatItOwns pins the package map contract: doc.go is the
// one file carrying the package doc comment, and it names what the package
// owns — the registry and rendering of managed Claude launches.
func TestPackageDocNamesWhatItOwns(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	carriers := []string{}
	var doc string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if file.Doc != nil {
			carriers = append(carriers, name)
			doc = file.Doc.Text()
		}
	}
	if len(carriers) != 1 || carriers[0] != "doc.go" {
		t.Fatalf("package doc carried by %v, want only doc.go", carriers)
	}
	const want = "Package claudelaunch owns the registry and rendering of managed Claude launches."
	if strings.TrimSpace(doc) != want {
		t.Fatalf("package doc = %q, want %q", doc, want)
	}
}
