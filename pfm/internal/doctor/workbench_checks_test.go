package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func writeDoctorBenchFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func doctorBenchFixture(t *testing.T) (root, dir, home string) {
	t.Helper()
	return doctorBenchFixtureAt(t, t.TempDir())
}

func doctorBenchFixtureAt(t *testing.T, base string) (root, dir, home string) {
	t.Helper()
	root = filepath.Join(base, "acme")
	dir = filepath.Join(root, "docs", "scribe")
	home = t.TempDir()
	writeDoctorBenchFile(t, professor.BaselinePath(root), "{}")
	writeDoctorBenchFile(
		t,
		paths.WorkbenchManifest(dir),
		`{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh","engines":["claude","codex"]}`,
	)
	writeDoctorBenchFile(t, filepath.Join(dir, ".professor", "scribe.md"), "You are scribe.")
	writeDoctorBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Scribe.\n")
	result, err := codexgen.Build(codexgen.Options{Root: dir, Home: home})
	if err != nil || !result.OK {
		t.Fatalf("build=%#v err=%v", result, err)
	}
	return root, dir, home
}

func TestWorkbenchDoctorMirrors(t *testing.T) {
	for _, state := range []string{"clean", "stale", "broken", "invalid"} {
		t.Run(state, func(t *testing.T) {
			root, dir, home := doctorBenchFixture(t)
			want := fmt.Sprintf(
				"doctor: workbench %s ok · engines claude,codex · prompt %s\n",
				dir,
				filepath.Join(dir, ".professor", "scribe.md"),
			)
			wantWarnings, wantFailures := 0, 0
			switch state {
			case "stale":
				writeDoctorBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Edited scribe.\n")
				result, err := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if err != nil {
					t.Fatal(err)
				}
				want += fmt.Sprintf(
					"doctor: workbench %s codex mirror STALE: %s — the next launch there rebuilds it\n",
					dir,
					result.Problems[0],
				)
				wantWarnings = 1
			case "broken":
				writeDoctorBenchFile(t, filepath.Join(dir, ".claude", "codex-build.json"), "{")
				_, err := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if err == nil {
					t.Fatal("broken fixture did not fail")
				}
				want += fmt.Sprintf("doctor: workbench %s codex mirror BROKEN: %v\n", dir, err)
				wantFailures = 1
			case "invalid":
				writeDoctorBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
				want = fmt.Sprintf(
					"doctor: workbench %s FAILED: %s: \"prompt\" is required\n",
					dir,
					paths.WorkbenchManifest(dir),
				)
				wantFailures = 1
			}
			var stdout bytes.Buffer
			warnings, failures := printWorkbenchDoctor(&stdout, root, home)
			if stdout.String() != want || warnings != wantWarnings || failures != wantFailures {
				t.Fatalf(
					"warnings=%d failures=%d output=%q, want %d %d %q",
					warnings,
					failures,
					stdout.String(),
					wantWarnings,
					wantFailures,
					want,
				)
			}
		})
	}
}

func TestWorkbenchDoctorWalkError(t *testing.T) {
	// A depth-6 directory whose path leaves no room for "/.professor" under
	// PATH_MAX fails its manifest probe (ENAMETOOLONG) for real, even in a
	// root-run fence; a regular file named .professor is absence, not a fault.
	const pathMax, segments, segment = 4095, 6, 255
	base := t.TempDir()
	for remaining := pathMax - 5 - segments*(segment+1) - len(base); remaining > 0; {
		n := min(200, remaining-1)
		if remaining-(n+1) == 1 {
			n--
		}
		base = filepath.Join(base, strings.Repeat("d", n))
		remaining -= n + 1
	}
	root, dir, home := doctorBenchFixtureAt(t, base)
	deep := root
	for range segments {
		deep = filepath.Join(deep, strings.Repeat("x", segment))
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	_, walkErrors := workbench.Discover([]string{root})
	if len(walkErrors) != 1 {
		t.Fatalf("walk errors=%v", walkErrors)
	}
	want := fmt.Sprintf(
		"doctor: workbench %s ok · engines claude,codex · prompt %s\n",
		dir,
		filepath.Join(dir, ".professor", "scribe.md"),
	) +
		fmt.Sprintf(
			"doctor: workbench discovery FAILED: %s — whether more workbenches exist there is UNKNOWN\n",
			walkErrors[0].Error(),
		)
	var stdout bytes.Buffer
	warnings, failures := printWorkbenchDoctor(&stdout, root, home)
	if stdout.String() != want || warnings != 0 || failures != 1 {
		t.Fatalf("warnings=%d failures=%d output=%q, want %q", warnings, failures, stdout.String(), want)
	}
}

func TestWorkbenchDoctorOutsideAndUnreadable(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			if broken {
				// A .professor symlink loop fails the baseline stat (ELOOP) for real.
				if err := os.Symlink(".professor", filepath.Join(root, ".professor")); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			warnings, failures := printWorkbenchDoctor(&stdout, root, home)
			want := ""
			wantFailures := 0
			if broken {
				_, _, err := professor.ResolveProjectRoot(root)
				if err == nil {
					t.Fatal("root fixture did not fail")
				}
				want = fmt.Sprintf("doctor: workbench UNREADABLE %v\n", err)
				wantFailures = 1
			}
			if stdout.String() != want || warnings != 0 || failures != wantFailures {
				t.Fatalf("warnings=%d failures=%d output=%q, want %q", warnings, failures, stdout.String(), want)
			}
		})
	}
}

func TestWorkbenchDoctorRunWiring(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	root, dir, _ := doctorBenchFixture(t)
	writeDoctorBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
	t.Chdir(root)
	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	want := fmt.Sprintf("doctor: workbench %s FAILED: %s: \"prompt\" is required\n", dir, paths.WorkbenchManifest(dir))
	if code != 3 || !strings.Contains(stdout.String(), want) {
		t.Fatalf("doctor code=%d missing %q: stdout=%s stderr=%s", code, want, stdout.String(), stderr.String())
	}
}
