package gate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchorsUseRecordedTreeEvenAfterHeadMoves(t *testing.T) {
	repo := newGitFixture(t)
	pinned := gitOutput(t, repo, "rev-parse", "HEAD")
	aHash := gitOutput(t, repo, "rev-parse", pinned+":a.txt")
	bHash := gitOutput(t, repo, "rev-parse", pinned+":b.txt")
	valid := canonicalGateMap(aHash[:12], bHash[:12], "a.txt")

	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("moved\n"), 0o600)
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "move head")

	result, err := Anchors(pinned, []MapInput{{Name: "valid-anchor.md", Text: valid}}, CommandGitReader{Repo: repo})
	if err != nil {
		t.Fatalf("Anchors() error = %v", err)
	}
	if len(result.Accepted) != 1 || result.Accepted[0] != "maps/valid-anchor.md" || len(result.Rejected) != 0 {
		t.Fatalf("Anchors() = %#v", result)
	}
}

func TestFlippedAnchorHashIsRejected(t *testing.T) {
	repo := newGitFixture(t)
	pinned := gitOutput(t, repo, "rev-parse", "HEAD")
	aHash := gitOutput(t, repo, "rev-parse", pinned+":a.txt")
	bHash := gitOutput(t, repo, "rev-parse", pinned+":b.txt")
	bad := "0" + aHash[1:12]
	if aHash[0] == '0' {
		bad = "1" + aHash[1:12]
	}
	result, err := Anchors(
		pinned,
		[]MapInput{{Name: "flipped-hash.md", Text: canonicalGateMap(bad, bHash[:12], "a.txt")}},
		CommandGitReader{Repo: repo},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 0 || len(result.Rejected) != 1 ||
		!strings.HasPrefix(result.Rejected[0].Reason, "anchor hash mismatch: a.txt") {
		t.Fatalf("Anchors() = %#v", result)
	}
}

func TestAnchorRejectionsAreMapLocalAndDeterministic(t *testing.T) {
	repo := newGitFixture(t)
	pinned := gitOutput(t, repo, "rev-parse", "HEAD")
	aHash := gitOutput(t, repo, "rev-parse", pinned+":a.txt")
	bHash := gitOutput(t, repo, "rev-parse", pinned+":b.txt")
	missingRange := canonicalGateMap(aHash[:12], bHash[:12], "a.txt:1-2,4-5")
	legacyHash := strings.Replace(canonicalGateMap(aHash[:12], bHash[:12], "a.txt"), aHash[:12], aHash, 1)
	result, err := Anchors(pinned, []MapInput{
		{Name: "z--bad.md", Text: canonicalGateMap(aHash[:12], bHash[:12], "a.txt")},
		{Name: "range.md", Text: missingRange},
		{Name: "legacy.md", Text: legacyHash},
	}, CommandGitReader{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejected) != 3 || result.Rejected[0].MapPath != "maps/legacy.md" ||
		result.Rejected[0].Reason != "anchor row grammar mismatch" ||
		result.Rejected[1].MapPath != "maps/range.md" ||
		result.Rejected[1].Reason != "anchor row grammar mismatch" ||
		result.Rejected[2].Reason != "invalid map filename" {
		t.Fatalf("Anchors() = %#v", result)
	}
}

func canonicalGateMap(firstHash, secondHash, firstPath string) string {
	return fmt.Sprintf(
		"# Gate fixture\n\n## Question\n\nDoes the anchor gate hold?\n\n## Answer\n\nYes.\n\n## Derivation trail\n\nA deterministic repository proves it.\n\nProvenance: 2026-08-13 · sid 0123abcd\n\n## Anchors\n\n- `%s` — blob `%s`\n- `b.txt` — blob `%s`\n",
		firstPath,
		firstHash,
		secondHash,
	)
}

func newGitFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.name", "Dream Test")
	gitRun(t, repo, "config", "user.email", "dream@test.invalid")
	for name, body := range map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "add", "a.txt", "b.txt")
	gitRun(t, repo, "commit", "-q", "-m", "fixture")
	return repo
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
