package dream

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath       = "hostops/pfm"
	dreamPrefix      = modulePath + "/internal/dream"
	dreamSeatPackage = dreamPrefix + "/seat"
	storePackage     = modulePath + "/internal/store"
	depsPackage      = modulePath + "/internal/deps"
)

var allowedSeatImports = map[string]struct{}{
	modulePath + "/internal/action":     {},
	modulePath + "/internal/engine":     {},
	modulePath + "/internal/spawn":      {},
	modulePath + "/internal/headless":   {},
	modulePath + "/internal/transcript": {},
	modulePath + "/internal/paths":      {},
}

var forbiddenDreamImports = map[string]struct{}{
	storePackage:                     {},
	modulePath + "/internal/ui":      {},
	modulePath + "/internal/compose": {},
	modulePath + "/internal/kill":    {},
	modulePath + "/internal/mcpserv": {},
	modulePath + "/internal/gather":  {},
}

type listedPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
}

func TestDreamPackagesRespectDirectImportBoundary(t *testing.T) {
	root := moduleRoot(t)
	packages := goList(t, root, "-deps", "-json", "./internal/dream/...")

	var dreamPackages []listedPackage
	for _, pkg := range packages {
		if pkg.ImportPath == dreamPrefix || strings.HasPrefix(pkg.ImportPath, dreamPrefix+"/") {
			dreamPackages = append(dreamPackages, pkg)
		}
	}
	if len(dreamPackages) == 0 {
		t.Fatal("go list returned no internal/dream packages; refusing to treat an empty scan as isolation")
	}

	for _, pkg := range dreamPackages {
		for _, imported := range pkg.Imports {
			// deps is a pure executable-resolution foundation: it imports no
			// host subsystem and is the one registry every exec site must use.
			if imported == depsPackage {
				continue
			}
			if _, forbidden := forbiddenDreamImports[imported]; forbidden {
				t.Errorf("%s directly imports forbidden host package %s", pkg.ImportPath, imported)
				continue
			}

			if !strings.HasPrefix(imported, modulePath+"/internal/") ||
				imported == dreamPrefix || strings.HasPrefix(imported, dreamPrefix+"/") {
				continue
			}
			if pkg.ImportPath != dreamSeatPackage {
				t.Errorf(
					"%s directly imports host package %s; only %s may cross the host boundary",
					pkg.ImportPath,
					imported,
					dreamSeatPackage,
				)
				continue
			}
			if _, allowed := allowedSeatImports[imported]; !allowed {
				t.Errorf("%s directly imports unapproved host package %s", pkg.ImportPath, imported)
			}
		}
	}
}

func TestDreamImportArrowIsOneWay(t *testing.T) {
	root := moduleRoot(t)
	packages := goList(t, root, "-json", "./...")
	if len(packages) == 0 {
		t.Fatal("go list returned no module packages; refusing to treat an empty source scan as isolation")
	}

	allowedFile := filepath.Join(root, "cmd", "pfm", "dream_command.go")
	filesScanned := 0
	for _, pkg := range packages {
		if pkg.ImportPath == dreamPrefix || strings.HasPrefix(pkg.ImportPath, dreamPrefix+"/") {
			continue
		}
		for _, name := range packageFiles(pkg) {
			filesScanned++
			filename := filepath.Join(pkg.Dir, name)
			for _, imported := range fileImports(t, filename) {
				if imported != dreamPrefix && !strings.HasPrefix(imported, dreamPrefix+"/") {
					continue
				}
				if filepath.Clean(filename) != allowedFile {
					rel, err := filepath.Rel(root, filename)
					if err != nil {
						rel = filename
					}
					t.Errorf(
						"%s imports %s; only cmd/pfm/dream_command.go may import internal/dream",
						filepath.ToSlash(rel),
						imported,
					)
				}
			}
		}
	}
	if filesScanned == 0 {
		t.Fatal("source scan visited no files; refusing to treat an empty scan as a one-way import arrow")
	}
}

func TestActionAndSpawnDoNotImportStore(t *testing.T) {
	// Engine identity lives in the standard-library-only engine leaf. Action
	// and spawn no longer need even a constants-only edge to the store.
	root := moduleRoot(t)
	packages := goList(t, root, "-json", "./internal/action", "./internal/spawn")
	byImportPath := make(map[string]listedPackage, len(packages))
	for _, pkg := range packages {
		byImportPath[pkg.ImportPath] = pkg
	}

	for _, importPath := range []string{
		modulePath + "/internal/action",
		modulePath + "/internal/spawn",
	} {
		pkg, ok := byImportPath[importPath]
		if !ok {
			t.Errorf("go list did not return required host package %s", importPath)
			continue
		}

		for _, imported := range pkg.Imports {
			if imported == storePackage {
				t.Errorf("%s still imports %s after engine identity moved to internal/engine", importPath, storePackage)
			}
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat go.mod in %s: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above test working directory")
		}
		dir = parent
	}
}

func goList(t *testing.T, root string, args ...string) []listedPackage {
	t.Helper()

	command := exec.Command("go", append([]string{"list"}, args...)...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(append([]string{"list"}, args...), " "), err, output)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var packages []listedPackage
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list output: %v\n%s", err, output)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func packageFiles(pkg listedPackage) []string {
	set := make(map[string]struct{})
	for _, files := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.TestGoFiles, pkg.XTestGoFiles} {
		for _, file := range files {
			set[file] = struct{}{}
		}
	}

	result := make([]string, 0, len(set))
	for file := range set {
		result = append(result, file)
	}
	sort.Strings(result)
	return result
}

func fileImports(t *testing.T, filename string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse imports in %s: %v", filename, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, filename, err)
		}
		imports = append(imports, path)
	}
	return imports
}
