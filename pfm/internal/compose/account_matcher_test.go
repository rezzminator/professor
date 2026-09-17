package compose

import (
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

func TestAccountAttributionUsesThirdAliasAndLongestNestedRoot(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	projects := filepath.Join(physical, "projects")
	nested := filepath.Join(projects, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasOne := filepath.Join(root, "alias-one")
	aliasTwo := filepath.Join(root, "alias-two")
	aliasThree := filepath.Join(root, "alias-three")
	for _, alias := range []string{aliasOne, aliasTwo, aliasThree} {
		if err := os.Symlink(physical, alias); err != nil {
			t.Fatal(err)
		}
	}
	transcriptPath := filepath.Join(aliasThree, "projects", "nested", "claude.jsonl")
	rolloutPath := filepath.Join(aliasThree, "projects", "nested", "rollout-codex.jsonl")
	output := Compose(Input{
		Transcripts: []store.Transcript{{
			UUID: "claude-alias", Path: transcriptPath, CWD: "/work/claude",
			FirstPrompt: "Claude alias", PromptCount: 1, Size: 10, MTimeNS: 1,
		}},
		Rollouts: []store.Rollout{{
			ID: "codex-alias", Path: rolloutPath, CWD: "/work/codex",
			FirstPrompt: "Codex alias", PromptCount: 1, Size: 10, MTimeNS: 2,
			UserThread: true,
		}},
		AccountRoots: []AccountRoot{
			{Account: 11, Path: filepath.Join(aliasOne, "projects")},
			{Account: 12, Path: filepath.Join(aliasOne, "projects", "nested")},
		},
		CodexHomes: []AccountRoot{
			{Account: 21, Path: filepath.Join(aliasOne, "projects")},
			{Account: 22, Path: filepath.Join(aliasOne, "projects", "nested")},
		},
		Options: Options{View: AllView},
	})
	claude, found := rowByID(output.Rows, "claude-alias")
	if !found || claude.Account != 12 {
		t.Fatalf("third-alias Claude row = %#v, want nested account 12", claude)
	}
	codex, found := rowByID(output.Rows, "codex-alias")
	if !found || codex.Account != 22 {
		t.Fatalf("third-alias Codex row = %#v, want nested account 22", codex)
	}
}

// A live agent's seat is the config dir its process runs under, even when
// every seat shares one transcript store (~/.cc/N/projects → ~/.claude/projects,
// the layout /reload across seats needs): the transcript path then matches
// every root and the first seat would otherwise claim every live chat.
func TestLiveAgentAccountFollowsItsConfigDir(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "claude")
	projects := filepath.Join(primary, "projects", "work")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	third := filepath.Join(root, "cc3")
	if err := os.MkdirAll(third, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(primary, "projects"), filepath.Join(third, "projects")); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(projects, "shared.jsonl")
	output := Compose(Input{
		Transcripts: []store.Transcript{{
			UUID: "shared", Path: transcriptPath, CWD: "/work",
			FirstPrompt: "shared store", PromptCount: 1, Size: 10, MTimeNS: 1,
		}},
		Snapshot: gather.Snapshot{Agents: []gather.Agent{{
			PID: 4242, Socket: "cc-shared", PaneID: "%0", SessionID: "shared", ConfigDir: third,
		}}},
		AccountRoots: []AccountRoot{
			{Account: 1, Path: filepath.Join(primary, "projects"), ConfigDir: primary},
			{Account: 3, Path: filepath.Join(third, "projects"), ConfigDir: third},
		},
		Options: Options{View: AllView},
	})
	row, found := rowByID(output.Rows, "shared")
	if !found || row.Account != 3 {
		t.Fatalf("live row on a shared store = %#v, want the process's seat 3", row)
	}
}
