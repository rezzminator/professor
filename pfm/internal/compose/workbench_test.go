package compose

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func workbenchInput(t *testing.T) Input {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "acme")
	scribe := filepath.Join(root, "docs", "scribe")
	lab := filepath.Join(root, ".professor", "lab")
	zeta := filepath.Join(base, "zeta")
	for _, dir := range []string{filepath.Join(root, ".git"), filepath.Join(root, "src"), filepath.Join(scribe, "notes"), lab, filepath.Join(zeta, ".git")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	benches := []workbench.Bench{
		{
			Dir:     scribe,
			Root:    root,
			Project: "acme",
			Title:   "Scribe",
			Key:     "acme › Scribe",
			Engines: []pfmengine.ID{pfmengine.Claude},
		},
		{Dir: lab, Root: root, Project: "acme", Title: "lab", Key: "acme › lab", Err: errors.New("invalid manifest")},
	}
	return Input{
		Workbenches: benches,
		ClaudeSeats: []ClaudeSeat{{Account: 2, ConfigDir: "/accounts/2"}},
		Options:     Options{View: AllView, PrimaryAccount: 2, CurrentDir: "/elsewhere"},
		Transcripts: []store.Transcript{
			transcript("A", "/accounts/2/projects/A.jsonl", filepath.Join(root, "src"), "A", 10, 1, 900),
			transcript("L1", "/accounts/2/projects/L1.jsonl", filepath.Join(scribe, "notes"), "L1", 10, 1, 950),
			transcript("Z", "/accounts/2/projects/Z.jsonl", zeta, "Z", 10, 1, 1000),
		},
	}
}

func TestWorkbenchFamilyOrder(t *testing.T) {
	input := workbenchInput(t)
	output := Compose(input)
	want := []string{"zeta", "acme", "acme › lab", "acme › Scribe"}
	if !reflect.DeepEqual(output.ProjectOrder, want) {
		t.Fatalf("ProjectOrder = %v, want %v", output.ProjectOrder, want)
	}
	var rows []string
	for _, row := range output.Rows {
		rows = append(rows, row.Kind.String()+":"+row.Name)
	}
	wantRows := []string{
		"new-claude:New Claude chat",
		"resume-claude:Z",
		"resume-claude:A",
		"workbench-invalid:invalid manifest",
		"new-claude:New Claude chat",
		"resume-claude:L1",
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("Rows = %v, want %v", rows, wantRows)
	}
}

func TestWorkbenchOwnership(t *testing.T) {
	input := workbenchInput(t)
	output := Compose(input)
	row, found := rowByID(output.Rows, "L1")
	if !found || row.Project != "acme › Scribe" || row.Workbench != input.Workbenches[0].Dir {
		t.Fatalf("L1 = %#v", row)
	}
	row, found = rowByID(output.Rows, "A")
	if !found || row.Project != "acme" || row.Workbench != "" {
		t.Fatalf("A = %#v", row)
	}
}

func TestWorkbenchNewRowEngines(t *testing.T) {
	for _, test := range []struct {
		name            string
		engines         []pfmengine.ID
		kind            Kind
		want            []pfmengine.ID
		account         int
		label           string
		codex, opencode bool
	}{
		{name: "claude", engines: []pfmengine.ID{pfmengine.Claude}, kind: NewClaude, want: []pfmengine.ID{pfmengine.Claude}, account: 2, label: "New Claude chat"},
		{name: "filter", engines: []pfmengine.ID{pfmengine.Codex, pfmengine.Claude}, kind: NewClaude, want: []pfmengine.ID{pfmengine.Claude}, account: 2, label: "New Claude chat"},
		{name: "codex first", engines: []pfmengine.ID{pfmengine.Codex, pfmengine.Claude}, kind: NewCodex, want: []pfmengine.ID{pfmengine.Codex, pfmengine.Claude}, account: 4, label: "New Codex chat", codex: true},
		{name: "opencode first", engines: []pfmengine.ID{pfmengine.OpenCode, pfmengine.Claude}, kind: NewOpenCode, want: []pfmengine.ID{pfmengine.OpenCode, pfmengine.Claude}, account: 5, label: "New OpenCode chat", opencode: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := workbenchInput(t)
			input.Workbenches[0].Engines = test.engines
			if test.codex {
				input.Options.CodexAccountIDs = []int{4}
				input.Options.PrimaryCodexAccount = 4
			}
			if test.opencode {
				input.Options.OpenCodeAccountIDs = []int{5}
				input.Options.PrimaryOpenCode = 5
			}
			output := Compose(input)
			for _, row := range output.Rows {
				if row.Project != "acme › Scribe" || row.ID != "" {
					continue
				}
				if row.Kind != test.kind || row.Name != test.label || row.CWD != input.Workbenches[0].Dir ||
					row.Workbench != row.CWD ||
					row.Account != test.account ||
					!reflect.DeepEqual(row.Engines, test.want) {
					t.Fatalf("new row = %#v", row)
				}
				return
			}
			t.Fatal("workbench new row missing")
		})
	}
}

func TestWorkbenchNoEngineAccount(t *testing.T) {
	input := workbenchInput(t)
	input.Workbenches[0].Engines = []pfmengine.ID{pfmengine.Codex}
	output := Compose(input)
	want := "workbench " + input.Workbenches[0].Dir + " enables codex, and this machine has no account for any of them"
	for _, row := range output.Rows {
		if row.Project == "acme › Scribe" && row.ID == "" {
			if row.Kind != WorkbenchInvalid || row.Name != want || row.Kind.IsAddressable() {
				t.Fatalf("unavailable row = %#v", row)
			}
			return
		}
	}
	t.Fatal("unavailable workbench row missing")
}

func TestWorkbenchChatlessFamilies(t *testing.T) {
	input := workbenchInput(t)
	input.Transcripts = input.Transcripts[2:]
	input.Options.CurrentDir = ""
	output := Compose(input)
	want := []string{"zeta", "acme", "acme › lab", "acme › Scribe"}
	if !reflect.DeepEqual(output.ProjectOrder, want) {
		t.Fatalf("chatless ProjectOrder = %v", output.ProjectOrder)
	}
	for _, row := range output.Rows {
		if row.Workbench == input.Workbenches[0].Dir && row.Kind == NewClaude {
			return
		}
	}
	t.Fatal("chatless workbench missing")
}

func TestWorkbenchOpenedInsideTargetsFamily(t *testing.T) {
	input := workbenchInput(t)
	input.Options.CurrentDir = input.Workbenches[0].Dir
	output := Compose(input)
	if !reflect.DeepEqual(output.ProjectOrder, []string{"acme", "acme › lab", "acme › Scribe", "zeta"}) {
		t.Fatalf("inside order = %v", output.ProjectOrder)
	}
	if row := output.Rows[0]; row.Project != "acme" || row.CWD != input.Workbenches[0].Root || row.Workbench != "" {
		t.Fatalf("top row = %#v", row)
	}
	if output.ProjectDirs["acme › Scribe"] != input.Workbenches[0].Dir {
		t.Fatalf("dirs = %v", output.ProjectDirs)
	}
}

func TestWorkbenchKilledView(t *testing.T) {
	input := workbenchInput(t)
	input.Options.View = KilledView
	input.WorkbenchErrors = []workbench.WalkError{
		{Root: input.Workbenches[0].Root, Path: "/work/acme/docs/locked", Err: errors.New("permission denied")},
	}
	for _, row := range Compose(input).Rows {
		if isNewChatKind(row.Kind) || row.Kind == WorkbenchInvalid {
			t.Fatalf("killed row = %#v", row)
		}
	}
}

func TestWorkbenchWalkError(t *testing.T) {
	input := workbenchInput(t)
	input.Workbenches = nil
	fault := workbench.WalkError{
		Root: filepath.Dir(input.Transcripts[0].CWD),
		Path: "/work/acme/docs/locked",
		Err:  errors.New("permission denied"),
	}
	input.WorkbenchErrors = []workbench.WalkError{fault}
	for _, row := range Compose(input).Rows {
		if row.Kind == WorkbenchInvalid {
			if row.Project != "acme" || row.Name != fault.Error() {
				t.Fatalf("walk row = %#v", row)
			}
			return
		}
	}
	t.Fatal("walk error missing")
}

func TestWorkbenchNestedManagedRootJoinsRepositoryFamily(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "acme")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repo, "apps", "portal")
	if err := os.MkdirAll(filepath.Dir(professor.BaselinePath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(professor.BaselinePath(root), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(root, "docs", "scribe")
	professorDir := filepath.Join(benchDir, ".professor")
	if err := os.MkdirAll(professorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(professorDir, "workbench.json"),
		[]byte(`{"prompt":"scribe.md","title":"Scribe"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(professorDir, "scribe.md"), []byte("You are scribe."), 0o600); err != nil {
		t.Fatal(err)
	}
	benches, walkErrors := workbench.Discover([]string{root})
	if len(walkErrors) != 0 || len(benches) != 1 {
		t.Fatalf("Discover = %#v, walk errors %v", benches, walkErrors)
	}

	input := workbenchInput(t)
	input.Workbenches = benches
	input.WorkbenchErrors = nil
	input.Transcripts = []store.Transcript{
		transcript("A", "/accounts/2/projects/A.jsonl", repo, "A", 10, 1, 900),
	}
	output := Compose(input)
	wantOrder := []string{"acme", "acme › Scribe"}
	if !reflect.DeepEqual(output.ProjectOrder, wantOrder) {
		t.Fatalf("ProjectOrder = %v, want %v", output.ProjectOrder, wantOrder)
	}
	for _, row := range output.Rows {
		if row.Project == "portal" {
			t.Fatalf("row filed under managed-root basename: %#v", row)
		}
	}

	input.Workbenches = nil
	input.WorkbenchErrors = []workbench.WalkError{{
		Root: root, Path: filepath.Join(root, "docs", "locked"), Err: errors.New("permission denied"),
	}}
	output = Compose(input)
	foundInvalid := false
	for _, row := range output.Rows {
		if row.Kind == WorkbenchInvalid {
			foundInvalid = true
			if row.Project != "acme" {
				t.Fatalf("walk row = %#v, want project acme", row)
			}
		}
	}
	if !foundInvalid {
		t.Fatal("walk error row missing")
	}
	for _, project := range output.ProjectOrder {
		if project == "portal" {
			t.Fatalf("ProjectOrder contains managed-root basename: %v", output.ProjectOrder)
		}
	}
}

func TestWorkbenchRepoRoots(t *testing.T) {
	input := workbenchInput(t)
	input.Options.CurrentDir = ""
	roots := Compose(input).RepoRoots()
	want := []string{input.Workbenches[0].Root, input.Workbenches[0].Dir, input.Transcripts[2].CWD}
	sort.Strings(want)
	if !reflect.DeepEqual(roots, want) {
		t.Fatalf("RepoRoots = %v, want %v", roots, want)
	}
}

func TestWorkbenchTwoClonesKeepOneGroupPerBench(t *testing.T) {
	base := t.TempDir()
	var roots []string
	var transcripts []store.Transcript
	for _, parent := range []string{"main", "spare"} {
		repo := filepath.Join(base, parent, "acme")
		professorDir := filepath.Join(repo, "docs", "scribe", ".professor")
		for _, dir := range []string{filepath.Join(repo, ".git"), filepath.Join(repo, "src"), professorDir} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for path, body := range map[string]string{
			professor.BaselinePath(repo):                  "{}",
			filepath.Join(professorDir, "workbench.json"): `{"prompt":"scribe.md","title":"Scribe"}`,
			filepath.Join(professorDir, "scribe.md"):      "You are scribe.",
		} {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		roots = append(roots, repo)
		transcripts = append(transcripts,
			transcript(parent, "/accounts/2/projects/"+parent+".jsonl", filepath.Join(repo, "src"), parent, 10, 1, 900))
	}
	benches, walkErrors := workbench.Discover(roots)
	if len(walkErrors) != 0 || len(benches) != 2 {
		t.Fatalf("Discover = %#v, walk errors %v", benches, walkErrors)
	}
	input := workbenchInput(t)
	input.Workbenches, input.WorkbenchErrors, input.Transcripts = benches, nil, transcripts
	output := Compose(input)
	seen := make(map[string]bool)
	for _, project := range output.ProjectOrder {
		if seen[project] {
			t.Fatalf("ProjectOrder lists %q twice: %v", project, output.ProjectOrder)
		}
		seen[project] = true
	}
	launchRows := make(map[string]int)
	for _, row := range output.Rows {
		if isNewChatKind(row.Kind) && row.Workbench != "" {
			launchRows[row.Workbench]++
		}
	}
	for i := range benches {
		if launchRows[benches[i].Dir] != 1 {
			t.Fatalf("bench %s has %d ✦ rows, want 1: %v", benches[i].Dir, launchRows[benches[i].Dir], launchRows)
		}
	}
}
