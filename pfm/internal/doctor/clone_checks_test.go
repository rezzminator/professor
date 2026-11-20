package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// writeCloneFile writes one file under the clone, its directories included.
func writeCloneFile(t *testing.T, clone, rel, content string) {
	t.Helper()
	path := filepath.Join(clone, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrintPfmBuildDoctor(t *testing.T) {
	const head, older = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	for _, test := range []struct {
		name         string
		build        pfmBuild
		ancestorExit int
		want         string
		warnings     int
		cloneVersion string
	}{
		{
			"unstamped",
			pfmBuild{Version: "dev", Revision: older},
			0,
			"doctor: pfm_build UNSTAMPED revision=" + older + " — this pfm carries no version stamp, so whether it is " +
				"behind {clone} (VERSION 0.89.0-alpha) is unknown — fix: make -C {clone}/pfm host-install\n", 1,
			"0.89.0-alpha",
		},
		{
			"behind by version",
			pfmBuild{Version: "0.88.0", Revision: older},
			0,
			"doctor: pfm_build BEHIND version=0.88.0 clone VERSION=0.89.0-alpha — fix: make -C {clone}/pfm host-install\n",
			1,
			"0.89.0-alpha",
		},
		{
			"ahead by version",
			pfmBuild{Version: "0.90.0", Revision: older},
			0,
			"doctor: pfm_build AHEAD version=0.90.0 clone VERSION=0.89.0-alpha — {clone} is older than this pfm — " +
				"fix: update the clone (git -C {clone} pull), then make -C {clone}/pfm host-install\n", 1,
			"0.89.0-alpha",
		},
		{
			"same version at HEAD",
			pfmBuild{Version: "0.89.0-alpha", Revision: head},
			0,
			"doctor: pfm_build ok version=0.89.0-alpha revision=111111111111 = HEAD\n", 0,
			"0.89.0-alpha",
		},
		{
			"release stamp with its v at HEAD",
			pfmBuild{Version: "v0.89.0-alpha", Revision: head},
			0,
			"doctor: pfm_build ok version=v0.89.0-alpha revision=111111111111 = HEAD\n", 0,
			"0.89.0-alpha",
		},
		{
			"release stamp with its v behind by version",
			pfmBuild{Version: "v0.88.0", Revision: older},
			0,
			"doctor: pfm_build BEHIND version=v0.88.0 clone VERSION=0.89.0-alpha — fix: make -C {clone}/pfm host-install\n",
			1,
			"0.89.0-alpha",
		},
		{
			"same version behind HEAD",
			pfmBuild{Version: "0.89.0-alpha", Revision: older, Modified: true},
			0,
			"doctor: pfm_build BEHIND version=0.89.0-alpha revision=222222222222 HEAD=111111111111 (built from a " +
				"modified tree) — fix: make -C {clone}/pfm host-install\n", 1,
			"0.89.0-alpha",
		},
		{
			"same version diverged",
			pfmBuild{Version: "0.89.0-alpha", Revision: older},
			1,
			"doctor: pfm_build DIVERGED version=0.89.0-alpha revision=222222222222 is not an ancestor of " +
				"HEAD=111111111111 — fix: make -C {clone}/pfm host-install\n", 1,
			"0.89.0-alpha",
		},
		{
			"same version without a revision",
			pfmBuild{Version: "0.89.0-alpha"},
			0,
			"doctor: pfm_build UNVERIFIED version=0.89.0-alpha revision=(none) — the binary carries no vcs.revision, " +
				"so whether it is behind HEAD is unknown — fix: make -C {clone}/pfm host-install\n", 1,
			"0.89.0-alpha",
		},
		{
			"release stamp without a revision at its release VERSION",
			pfmBuild{Version: "v0.89.0"},
			0,
			"doctor: pfm_build ok version=v0.89.0 = clone VERSION 0.89.0 (a release: its VERSION names one commit)\n",
			0,
			"0.89.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := t.TempDir()
			writeCloneFile(t, clone, "VERSION", test.cloneVersion+"\n")
			runner := &deps.FakeRunner{}
			runner.Script(
				[]string{"git", "-C", clone, "rev-parse", "HEAD"},
				deps.RunResult{Stdout: []byte(head + "\n")},
				nil,
			)
			runner.Script([]string{"git", "-C", clone, "merge-base", "--is-ancestor"},
				deps.RunResult{ExitCode: test.ancestorExit}, nil)
			var out bytes.Buffer
			warnings := printPfmBuildDoctor(context.Background(), &out, clone, test.build, runner)
			want := bytes.ReplaceAll([]byte(test.want), []byte("{clone}"), []byte(clone))
			if out.String() != string(want) || warnings != test.warnings {
				t.Fatalf("got %q warnings=%d\nwant %q warnings=%d", out.String(), warnings, want, test.warnings)
			}
		})
	}
}

// TestPrintProfessorCloneDoctorWithoutAClone: a home with no clone prints one
// named row and counts nothing; a copy that is no git checkout runs only
// leak_terms; a checkout runs the three rows.
func TestPrintProfessorCloneDoctorWithoutAClone(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	warnings := printProfessorCloneDoctor(
		context.Background(), &out, home, pfmBuild{}, &deps.FakeRunner{}, warningFilter{},
	)
	want := "doctor: professor_clone none at " + filepath.Join(home, ".professor") +
		" — pfm_build, git_push and leak_terms do not apply\n"
	if out.String() != want || warnings != 0 {
		t.Fatalf("got %q warnings=%d, want %q", out.String(), warnings, want)
	}
	clone := filepath.Join(home, ".professor")
	writeCloneFile(t, clone, "VERSION", "0.89.0-alpha\n")
	out.Reset()
	warnings = printProfessorCloneDoctor(
		context.Background(),
		&out,
		home,
		pfmBuild{Version: "0.88.0"},
		&deps.FakeRunner{},
		warningFilter{},
	)
	want = "doctor: professor_clone " + clone + " is not a git checkout — pfm_build and git_push do not apply\n" +
		"doctor: leak_terms does not apply: " + clone + " has no scripts/leak-check.sh\n"
	if out.String() != want || warnings != 0 {
		t.Fatalf("copy without .git: got %q warnings=%d\nwant %q", out.String(), warnings, want)
	}
	writeCloneFile(t, clone, ".git", "gitdir: /elsewhere\n")
	out.Reset()
	runner := &deps.FakeRunner{}
	runner.Script([]string{"git", "-C", clone, "remote", "get-url", "origin"},
		deps.RunResult{Stdout: []byte("https://example.invalid/acme/professor.git\n")}, nil)
	warnings = printProfessorCloneDoctor(
		context.Background(),
		&out,
		home,
		pfmBuild{Version: "0.88.0"},
		runner,
		warningFilter{},
	)
	want = "doctor: pfm_build BEHIND version=0.88.0 clone VERSION=0.89.0-alpha — fix: make -C " +
		filepath.Join(clone, "pfm") + " host-install\n" +
		"doctor: git_push does not apply: origin https://example.invalid/acme/professor.git is not on github.com\n" +
		"doctor: leak_terms does not apply: " + clone + " has no scripts/leak-check.sh\n"
	if out.String() != want || warnings != 1 {
		t.Fatalf("got %q warnings=%d\nwant %q", out.String(), warnings, want)
	}
}

func ptr(value string) *string { return &value }
