package dream

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/prompts"
)

func TestRuntimePromptsCarryTheMechanicalArtifactLaws(t *testing.T) {
	distill, err := fs.ReadFile(prompts.Dreamer(), distillPromptFile)
	if err != nil {
		t.Fatal(err)
	}
	refiner, err := fs.ReadFile(prompts.Dreamer(), refinerPromptFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, law := range []string{
		"never emit a commit sha",
		"REVIEW TRIGGERS, not citations",
		"multiple regions use separate anchor rows or the bare file path",
	} {
		if !strings.Contains(string(distill), law) {
			t.Errorf("distill prompt lacks %q", law)
		}
	}
	for _, law := range []string{"REVIEW TRIGGER", "2–8 anchor rows and no more"} {
		if !strings.Contains(string(refiner), law) {
			t.Errorf("refiner prompt lacks %q", law)
		}
	}
}

func TestLiveOrgansCarryNoRetiredAnchorRows(t *testing.T) {
	for _, repository := range liveContractRepositories(t) {
		organRoot := filepath.Join(repository.RepoRoot, ".professor", "stm")
		mapsRoot := filepath.Join(organRoot, "maps")
		if _, err := os.Stat(mapsRoot); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(mapsRoot)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(mapsRoot, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "git log -1") {
				t.Errorf("live map carries retired anchor grammar: %s", filepath.Join(mapsRoot, entry.Name()))
			}
		}
	}
}

func TestLiveExplorerSurfaceRegeneratesByteIdentically(t *testing.T) {
	var organRoot string
	for _, repository := range liveContractRepositories(t) {
		candidate := filepath.Join(repository.RepoRoot, ".professor", "stm")
		if info, err := os.Stat(
			filepath.Join(candidate, "agents", "tracer.md"),
		); err == nil &&
			info.Mode().IsRegular() {
			organRoot = candidate
			break
		}
	}
	if organRoot == "" {
		t.Fatal("live repository list contains no generated explorer surface")
	}
	for _, path := range []string{
		filepath.Join(organRoot, "maps"),
		filepath.Join(organRoot, "stm.md"),
		filepath.Join(organRoot, "lanes.tsv"),
		filepath.Join(organRoot, "agents", "tracer.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	stmRaw, err := os.ReadFile(filepath.Join(organRoot, "stm.md"))
	if err != nil {
		t.Fatal(err)
	}
	lanesRaw, err := os.ReadFile(filepath.Join(organRoot, "lanes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	membership, err := artifact.ParseLaneMembership(string(lanesRaw))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := lane.RenderSurfaces(filepath.Join(organRoot, "maps"), string(stmRaw), membership)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(organRoot, "agents", "tracer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Agents["tracer"] != string(want) {
		t.Fatal("live explorer surface does not byte-match deterministic regeneration")
	}
}

func liveContractRepositories(t *testing.T) []morningRepository {
	t.Helper()
	if os.Getenv("DREAM_LIVE_CONTRACT") != "1" {
		t.Skip("set DREAM_LIVE_CONTRACT=1 to verify the configured live organs")
	}
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configRoot) {
		configRoot = filepath.Join(os.Getenv("HOME"), ".config")
	}
	repositories, err := readMorningRepositories(
		filepath.Join(filepath.Clean(configRoot), "pfm", "repos.list"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) == 0 {
		t.Fatal("live repository list is empty")
	}
	return repositories
}

// Every lane that has a surface must have a hook wired to deliver it. A lane can
// accumulate maps for months while the Codex SubagentStart matcher never names
// its agent type: the hook is correct, the surface is correct, and the agent is
// simply never asked — indistinguishable from a lane with no memory. This walks
// the live organs and fails naming any lane whose delivery is unwired, so the
// next agent added under the same pattern cannot repeat it silently.
func TestLiveLaneSurfacesAreAllDeliverable(t *testing.T) {
	repositories := liveContractRepositories(t)

	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read codex hooks.json: %v", err)
	}
	var config struct {
		Hooks struct {
			SubagentStart []struct {
				Matcher string `json:"matcher"`
			} `json:"SubagentStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parse codex hooks.json: %v", err)
	}
	matchers := make(map[string]struct{}, len(config.Hooks.SubagentStart))
	for _, entry := range config.Hooks.SubagentStart {
		matchers[entry.Matcher] = struct{}{}
	}
	if len(matchers) == 0 {
		t.Fatal("codex hooks.json declares no SubagentStart matcher: no lane is deliverable")
	}

	inspected, unwired := 0, []string(nil)
	for _, repository := range repositories {
		agentsDir := filepath.Join(repository.RepoRoot, ".professor", "stm", "agents")
		entries, err := os.ReadDir(agentsDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", agentsDir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".md") {
				continue
			}
			laneName := strings.TrimSuffix(name, ".md")
			inspected++
			// A matcher covers the lane when it names the lane directly, or names
			// an agent type that normalizes onto it (Explore -> explorer).
			covered := false
			for matcher := range matchers {
				if matcher == laneName {
					covered = true
					break
				}
				if normalized, err := lane.FromAgentType(matcher); err == nil && normalized == laneName {
					covered = true
					break
				}
			}
			if !covered {
				unwired = append(unwired, repository.RepoRoot+" lane "+laneName)
			}
		}
	}
	// An empty enumeration is never a verdict: say what was inspected.
	t.Logf("inspected %d lane surfaces across %d configured repositories; matchers: %d",
		inspected, len(repositories), len(matchers))
	if inspected == 0 {
		t.Fatal("no lane surface was inspected: the check proved nothing")
	}
	if len(unwired) > 0 {
		sort.Strings(unwired)
		t.Fatalf("lane surfaces with no hook matcher to deliver them:\n  %s", strings.Join(unwired, "\n  "))
	}
}
