package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestUpdateCheckAlias(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		config     bool
		wantCode   int
		wantStderr string
		jsonOutput bool
	}{
		{
			name:       "report without baseline",
			wantCode:   3,
			wantStderr: "pfm update check: kept for older instructions; the current command is pfm doctor --project-updates\n",
			jsonOutput: true,
		},
		{
			name:       "stray positional",
			args:       []string{"check", "extra"},
			wantCode:   2,
			wantStderr: "usage: pfm update check [--root DIR] [--json]\n",
		},
		{
			name:       "config unreadable",
			args:       []string{"check"},
			config:     true,
			wantCode:   3,
			wantStderr: "pfm update check: config: ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := updateTestRuntime(t)
			runtimes := []pfmconfig.Runtime{runtime}
			args := tc.args
			if tc.jsonOutput {
				args = []string{"check", "--root", t.TempDir(), "--json"}
			}
			if tc.config {
				path := filepath.Join(t.TempDir(), "pfm.config.json")
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvConfig, path)
				runtimes = nil
			}
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr, runtimes...); code != tc.wantCode {
				t.Fatalf("Run() code = %d, want %d; stderr = %q", code, tc.wantCode, stderr.String())
			}
			if tc.config {
				if !strings.HasPrefix(stderr.String(), tc.wantStderr) {
					t.Fatalf("stderr = %q, want prefix %q", stderr.String(), tc.wantStderr)
				}
			} else if stderr.String() != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
			if !tc.jsonOutput {
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty", stdout.String())
				}
				return
			}
			var report struct {
				Terminal string `json:"terminal"`
			}
			decoder := json.NewDecoder(&stdout)
			if err := decoder.Decode(&report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if !strings.HasPrefix(report.Terminal, "FAILED — .professor/baseline.json not found") {
				t.Fatalf("terminal = %q, want baseline failure", report.Terminal)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("after one report: %v, want EOF", err)
			}
		})
	}
}

func TestUpdateCheckAliasReportsRetiredNames(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	writeProjectFixtureFile(t, fixture.project, "legacy.md", "Use /wave:refine.\n")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"check", "--root", fixture.project}, &stdout, &stderr, fixture.runtime)
	if got, want := strings.NewReplacer(fixture.project, "{project}", fixture.store, "{store}").
		Replace(stdout.String()),
		"professor: {project}  blueprint 0d181b1 → 0303110\n  current       1\n  ignored       0\n  UPDATED       2\n    .claude/updated-one.md   project/updated-one.md  pinned @0d181b1\n      upstream change: git -C {store} diff 0d181b1 -- templates/project/updated-one.md\n      upstream commits that changed it since the pin, oldest first — port each one's intent:\n        0303110 new templates\n      diff --git a/templates/project/updated-one.md b/templates/project/updated-one.md\n      index a90e482..26dfb4c 100644\n      --- a/templates/project/updated-one.md\n      +++ b/templates/project/updated-one.md\n      @@ -1 +1 @@\n      -old one\n      +new one\n      port what applies into .claude/updated-one.md, keep the project's own edits, then: pfm update pin .claude/updated-one.md\n      or keep your version on purpose, on record: pfm update pin .claude/updated-one.md --keep \"<what you did not take, and why>\"\n    .claude/updated-two.md   project/updated-two.md  pinned @0d181b1\n      upstream change: git -C {store} diff 0d181b1 -- templates/project/updated-two.md\n      upstream commits that changed it since the pin, oldest first — port each one's intent:\n        0303110 new templates\n      diff --git a/templates/project/updated-two.md b/templates/project/updated-two.md\n      index bec93f6..5f11179 100644\n      --- a/templates/project/updated-two.md\n      +++ b/templates/project/updated-two.md\n      @@ -1 +1 @@\n      -old two\n      +new two\n      port what applies into .claude/updated-two.md, keep the project's own edits, then: pfm update pin .claude/updated-two.md\n      or keep your version on purpose, on record: pfm update pin .claude/updated-two.md --keep \"<what you did not take, and why>\"\n  NEW           1\n    project/new.md — adopt: copy/adapt it locally, then pfm update pin --template project/new.md <local> — or ignore\n  GONE-UPSTREAM 1\n    .claude/gone.md   project/gone.md — retired upstream — delete it and pfm update drop .claude/gone.md; keep it and drop only its pin if the project still uses it\n  MOVED         0\n  LOCAL-DELETED 1\n    .claude/deleted.md   project/deleted.md — pfm update drop .claude/deleted.md to forget, or restore the file\n  RETIRED-NAME  1\n    legacy.md:1   /wave — retired command, now the flights:foreman agent\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nREVIEW REQUIRED — 6 items; nothing was written.\n"; code != 1 ||
		got != want {
		t.Fatalf("alias stdout = %q, want %q (exit %d stderr %q)", got, want, code, stderr.String())
	}
}

func TestUpdateJSONIsExactlyOneObject(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		name := "human"
		if jsonOutput {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			repo := newUpdateGitFixture(t)
			runtime := updateTestRuntime(t)
			stubUpdatePipeline(t, runtime)
			updateBaselineDoctor = func(_ context.Context, _ pfmconfig.Runtime, stdout, _ io.Writer) (doctorOutcome, error) {
				fmt.Fprintln(stdout, "baseline doctor progress")
				return doctorOutcome{}, nil
			}
			updateApplyInstall = func(_ context.Context, _, _, _ string, _ pfmconfig.Runtime, stdout, stderr io.Writer) error {
				fmt.Fprintln(stdout, "install progress")
				fmt.Fprintln(stderr, "install diagnostic")
				return nil
			}
			updateRunDoctor = func(_ context.Context, _ string, _ pfmconfig.Runtime, _ string, stdout, _ io.Writer) (doctorOutcome, error) {
				output := "doctor: row=new warning\ndoctor: warnings=1\n"
				fmt.Fprint(stdout, output)
				return doctorOutcome{Exit: 1, Warnings: 1, Output: output}, nil
			}
			args := []string{"--repo", repo, "--root", t.TempDir()}
			if jsonOutput {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr, runtime); code != 0 {
				t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			progress := "baseline doctor progress\ninstall progress\ndoctor: row=new warning\ndoctor: warnings=1\n" +
				"doctor after update: warnings=1 (before update: 0)\n" +
				"new warning rows — read them before the next update:\ndoctor: row=new warning\n" +
				fmt.Sprintf("updated v0.10.0 from %s\nrelease notes: none between v0.9.0 and v0.10.0\n", repo)
			if !jsonOutput {
				if stderr.String() != "install diagnostic\n" ||
					!strings.HasPrefix(stdout.String(), progress+"NOT-MANAGED — ") {
					t.Fatalf("human output changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				return
			}
			decoder := json.NewDecoder(&stdout)
			var report map[string]any
			if err := decoder.Decode(&report); err != nil {
				t.Fatalf("stdout is not one JSON object: %v", err)
			}
			if terminal, ok := report["terminal"].(string); !ok || !strings.HasPrefix(terminal, "NOT-MANAGED — ") {
				t.Fatalf("JSON terminal=%v, want NOT-MANAGED", report["terminal"])
			}
			if err := decoder.Decode(&report); err != io.EOF {
				t.Fatalf("second JSON decode=%v, want io.EOF", err)
			}
			wantStderr := strings.Replace(progress, "install progress\n", "install progress\ninstall diagnostic\n", 1)
			if stderr.String() != wantStderr {
				t.Fatalf("JSON progress stderr=%q, want %q", stderr.String(), wantStderr)
			}
		})
	}
}
