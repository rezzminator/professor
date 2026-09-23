package cmdparse

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestPythonHeredocAttributesOpenedFile(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "data.json")
	command := "cd . && python3 - <<'EOF'\nimport json\nwith open(\"data.json\") as f:\n    print(json.load(f))\nEOF"
	parts := parseOne(t, cwd, command, nil)
	var py *Part
	for i := range parts {
		if parts[i].Lang == LangPython {
			py = &parts[i]
		}
	}
	if py == nil {
		t.Fatalf("no python part in %+v", parts)
	}
	if py.Status != StatusOK || py.Program != "python3" {
		t.Fatalf("python part = %+v, want ok python3", *py)
	}
	assertFiles(t, []Part{*py}, []FileRef{ref(cwd, "data.json", ActionReadWhole, "")})
}

func TestPythonDashCOpenForWriteIsAWrite(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x.txt")
	parts := parseOne(t, cwd, `python3 -c "open('x.txt','w')"`, nil)
	if len(parts) != 1 || parts[0].Lang != LangPython || parts[0].Status != StatusOK {
		t.Fatalf("parts = %+v, want one ok python part", parts)
	}
	assertFiles(t, parts, []FileRef{ref(cwd, "x.txt", ActionWrite, "")})
}

func TestPythonSyntaxErrorIsAnErrorPart(t *testing.T) {
	t.Parallel()
	cwd := fixture(t)
	parts := parseOne(t, cwd, `python3 -c "def (:"`, nil)
	if len(parts) != 1 || parts[0].Status != StatusError || !strings.Contains(parts[0].Error, "SyntaxError") {
		t.Fatalf("parts = %+v, want one error part carrying SyntaxError", parts)
	}
}

func TestMissingInterpreterMarksPythonUnavailable(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x.txt")
	py := Python3{Runner: deps.RealRunner{}, Program: "pfm-cmdparse-no-such-python"}
	got, err := ParseBatch(context.Background(), []Call{
		{ID: "a", Command: `python3 -c "open('x.txt')"`, Cwd: cwd},
		{ID: "b", Command: "cat x.txt; python3 <<EOF\nprint(1)\nEOF", Cwd: cwd},
	}, py)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	var unavailable int
	for id, parts := range got {
		for _, p := range parts {
			switch p.Lang {
			case LangPython:
				if p.Status != StatusPythonUnavailable || !strings.Contains(p.Error, "pfm-cmdparse-no-such-python") {
					t.Fatalf("call %s python part = %+v, want python-unavailable naming the cause", id, p)
				}
				unavailable++
			case LangSh:
				if p.Status != StatusOK {
					t.Fatalf("call %s shell part = %+v, want ok", id, p)
				}
			}
		}
	}
	if unavailable != 2 {
		t.Fatalf("python-unavailable parts = %d, want 2 in %+v", unavailable, got)
	}
}

// A Python open() or Path(...) write of a file that does not exist yet is
// attributed, Exists false, in the directory the part ran in; a bare string
// naming nothing on disk stays unattributed.
func TestPythonWriteOfAMissingFile(t *testing.T) {
	t.Parallel()
	cwd := fixture(t)
	if err := os.Mkdir(filepath.Join(cwd, "X"), 0o700); err != nil {
		t.Fatalf("mkdir X: %v", err)
	}
	command := "cd X && python3 - <<'EOF'\nopen('f', 'w').write('x')\nfrom pathlib import Path\n" +
		"Path('g').write_text('y')\nprint('h')\nEOF"
	parts := parseOne(t, cwd, command, nil)
	var files []FileRef
	for _, p := range parts {
		if p.Lang == LangPython {
			if p.Status != StatusOK {
				t.Fatalf("python part = %+v, want ok", p)
			}
			files = append(files, p.Files...)
		}
	}
	assertFiles(t, []Part{{Files: files}}, []FileRef{
		// The scanner walks the tree breadth first: g's call sits higher.
		missing(cwd, "X/g", ActionWrite, ""),
		missing(cwd, "X/f", ActionWrite, ""),
	})
}
