package dream

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/lane"
)

func TestClaudeHookPreservesOrderedToolInputAndAnnotatesDrift(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"), "- Subject -> maps/subject.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(t, filepath.Join(organRoot, "maps", "subject.md"), hookMap(firstHash, secondHash))

	writeHookFile(t, filepath.Join(repository, "anchor.txt"), "moved\n")
	hookGit(t, repository, "add", "anchor.txt")
	hookGit(t, repository, "commit", "-m", "move anchor")

	input := []byte(
		`{"tool_input":{"z_unknown":{"nested":true},"subagent_type":"Explore","prompt":"original","a_unknown":7}}`,
	)
	got, err := Hook(HookRequest{Kind: HookAgentInject, Input: input, ProjectDirectory: repository})
	if err != nil {
		t.Fatalf("Hook() error = %v", err)
	}
	wantPrefix := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"z_unknown":{"nested":true},"subagent_type":"Explore","prompt":`
	if !strings.HasPrefix(string(got), wantPrefix) || !strings.Contains(string(got), `,"a_unknown":7}}}`) {
		t.Fatalf("ordered hook output = %s", got)
	}
	if !strings.Contains(string(got), `⚠ DRIFTED (1/2 anchors moved: anchor.txt)`) ||
		!strings.HasSuffix(string(got), "\n") {
		t.Fatalf("Claude hook lacks drift/newline: %s", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("hook JSON = %v", err)
	}
}

func TestBothHookPathsUseOneExploreLaneNormalizer(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"), "- Subject -> maps/subject.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(t, filepath.Join(organRoot, "maps", "subject.md"), hookMap(firstHash, secondHash))

	claude, err := Hook(HookRequest{
		Kind: HookAgentInject, ProjectDirectory: repository,
		Input: []byte(`{"tool_input":{"subagent_type":"Explore","prompt":"go"}}`),
	})
	if err != nil || len(claude) == 0 {
		t.Fatalf("Claude Explore = %q, %v", claude, err)
	}
	codex, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"Explore","cwd":"` + repository + `"}`),
	})
	if err != nil || len(codex) == 0 {
		t.Fatalf("Codex Explore = %q, %v", codex, err)
	}
	if !strings.Contains(string(codex), "Subject -> maps/subject.md") || strings.Contains(string(codex), "⚠ DRIFTED") {
		t.Fatalf("Codex hook surface = %s", codex)
	}
}

func TestHookNativeWireBytesArePinned(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "qa.md"), "- Subject -> maps/subject.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(t, filepath.Join(organRoot, "maps", "subject.md"), hookMap(firstHash, secondHash))

	claude, err := Hook(HookRequest{
		Kind:             HookAgentInject,
		ProjectDirectory: repository,
		Input: []byte(
			`{"session_id":"ignored","tool_input":{"subagent_type":"qa","prompt":"do it","description":"kept"}}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantClaude := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"subagent_type":"qa","prompt":"do it\n\nCached maps for this repository (bodies under ` + organRoot + `/maps/). Consult a covering map before re-deriving its subject and cite it when used. A DRIFTED mark names the moved anchors: if you open that map, re-verify its claims against those files at HEAD, then repair it — claims intact → run ` + "`" + `pfm dream restamp maps/{slug}.md` + "`" + `; claims changed → rewrite the map body, then the same restamp; cannot verify now → append a FLAG line to ` + organRoot + `/stm.md:\n- Subject -> maps/subject.md","description":"kept"}}}` + "\n"
	if string(claude) != wantClaude {
		t.Fatalf("Claude hook bytes = %q, want %q", claude, wantClaude)
	}

	codex, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := `{"hookSpecificOutput":{"hookEventName":"SubagentStart","additionalContext":"Cached maps for this repository (bodies under ` + organRoot + `/maps/). Consult a covering map before re-deriving its subject and cite it when used. A DRIFTED mark names the moved anchors: if you open that map, re-verify its claims against those files at HEAD, then repair it — claims intact → run ` + "`" + `pfm dream restamp maps/{slug}.md` + "`" + `; claims changed → rewrite the map body, then the same restamp; cannot verify now → append a FLAG line to ` + organRoot + `/stm.md:\n- Subject -> maps/subject.md"}}` + "\n"
	if string(codex) != wantCodex {
		t.Fatalf("Codex hook bytes = %q, want %q", codex, wantCodex)
	}
}

func TestClaudeHookFallsBackToLegacyExplorerIndex(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "explorer-index.md"), "- Legacy -> maps/legacy.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(
		t,
		filepath.Join(organRoot, "maps", "legacy.md"),
		strings.Replace(hookMap(firstHash, secondHash), "# Subject", "# Legacy", 1),
	)
	got, err := Hook(
		HookRequest{
			Kind:             HookAgentInject,
			ProjectDirectory: repository,
			Input:            []byte(`{"tool_input":{"subagent_type":"Explore","prompt":"go"}}`),
		},
	)
	if err != nil || !strings.Contains(string(got), "Legacy -> maps/legacy.md") {
		t.Fatalf("legacy fallback = %s, %v", got, err)
	}
}

func TestClaudeHookPrefersGeneratedExplorerSurfaceOverLegacyFallback(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"), "- Current -> maps/current.md\n")
	writeHookFile(t, filepath.Join(organRoot, "explorer-index.md"), "- Legacy -> maps/legacy.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(
		t,
		filepath.Join(organRoot, "maps", "current.md"),
		strings.Replace(hookMap(firstHash, secondHash), "# Subject", "# Current", 1),
	)
	writeHookFile(
		t,
		filepath.Join(organRoot, "maps", "legacy.md"),
		strings.Replace(hookMap(firstHash, secondHash), "# Subject", "# Legacy", 1),
	)
	got, err := Hook(
		HookRequest{
			Kind:             HookAgentInject,
			ProjectDirectory: repository,
			Input:            []byte(`{"tool_input":{"subagent_type":"Explore","prompt":"go"}}`),
		},
	)
	if err != nil || !strings.Contains(string(got), "Current -> maps/current.md") ||
		strings.Contains(string(got), "Legacy ->") {
		t.Fatalf("generated surface preference = %s, %v", got, err)
	}
}

func TestMalformedNonemptySurfaceFailsLoudly(t *testing.T) {
	repository := hookRepository(t)
	writeHookFile(t, filepath.Join(repository, ".professor", "stm", "agents", "qa.md"), "not a surface\n")
	_, err := Hook(
		HookRequest{Kind: HookCodexSubagentInject, Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`)},
	)
	if err == nil || !strings.Contains(err.Error(), "validate lane surface") {
		t.Fatalf("malformed surface error = %v", err)
	}
}

func TestHooksMigrateLegacyLaneSurfacesBeforeConsult(t *testing.T) {
	for _, test := range []struct {
		name       string
		kind       HookKind
		body       string
		wantRows   []string
		wantMaps   []string
		wantAnchor map[string]int
	}{
		{
			name: "Claude dated",
			kind: HookAgentInject,
			body: lane.AgentSurfaceHeader + "\n" +
				"- 2026-08-01 Cache policy -> Evict derived entries after the source changes.\n",
			wantRows: []string{
				"- Evict derived entries after the source changes. -> maps/cache-policy.md",
			},
			wantMaps:   []string{"cache-policy.md"},
			wantAnchor: map[string]int{"cache-policy.md": 2},
		},
		{
			name: "Codex undated",
			kind: HookCodexSubagentInject,
			body: lane.AgentSurfaceHeader + "\n" +
				"- Retry boundary -> Retry only the idempotent operation.\n",
			wantRows: []string{
				"- Retry only the idempotent operation. -> maps/retry-boundary.md",
			},
			wantMaps:   []string{"retry-boundary.md"},
			wantAnchor: map[string]int{"retry-boundary.md": 2},
		},
		{
			name: "mixed modern and legacy",
			kind: HookCodexSubagentInject,
			body: lane.AgentSurfaceHeader + "\n" +
				"- Keep the existing pointer. -> maps/existing.md\n" +
				"- 2026-08-02 Queue ownership -> One worker owns each queue item.\n",
			wantRows: []string{
				"- Keep the existing pointer. -> maps/existing.md",
				"- One worker owns each queue item. -> maps/queue-ownership.md",
			},
			wantMaps:   []string{"existing.md", "queue-ownership.md"},
			wantAnchor: map[string]int{"queue-ownership.md": 3},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := hookRepository(t)
			organRoot := filepath.Join(repository, ".professor", "stm")
			surfacePath := filepath.Join(organRoot, "agents", "qa.md")
			if strings.Contains(test.body, "maps/existing.md") {
				firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
				secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
				writeHookFile(t, filepath.Join(organRoot, "maps", "existing.md"), hookMap(firstHash, secondHash))
			}
			writeHookFile(t, surfacePath, test.body)
			hookGit(t, repository, "add", ".professor/stm")
			hookGit(t, repository, "commit", "-q", "-m", "legacy lane surface")
			blob := hookGit(t, repository, "rev-parse", "HEAD:.professor/stm/agents/qa.md")[:12]

			request := HookRequest{
				Kind:  test.kind,
				Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`),
			}
			if test.kind == HookAgentInject {
				request.ProjectDirectory = repository
				request.Input = []byte(`{"tool_input":{"subagent_type":"qa","prompt":"go"}}`)
			}
			output, err := Hook(request)
			if err != nil {
				t.Fatalf("Hook() error = %v", err)
			}
			for _, row := range test.wantRows {
				if !strings.Contains(string(output), row) {
					t.Fatalf("hook output lacks %q: %s", row, output)
				}
			}

			surface := string(mustReadHookFile(t, surfacePath))
			if _, err := lane.ValidSurface(surface); err != nil {
				t.Fatalf("migrated surface is invalid: %v\n%s", err, surface)
			}
			assertHookMode(t, surfacePath, 0o600)
			for _, name := range test.wantMaps {
				path := filepath.Join(organRoot, "maps", name)
				assertHookMode(t, path, 0o600)
				line, migrated := test.wantAnchor[name]
				if !migrated {
					continue
				}
				body := string(mustReadHookFile(t, path))
				wantAnchor := "- `.professor/stm/agents/qa.md:" + strconv.Itoa(line) + "` — blob `" + blob + "`"
				if !strings.Contains(body, wantAnchor) {
					t.Fatalf("map %s lacks source-row anchor %q:\n%s", name, wantAnchor, body)
				}
			}

			beforeSurface := string(mustReadHookFile(t, surfacePath))
			beforeMaps := hookMapSnapshot(t, filepath.Join(organRoot, "maps"))
			if _, err := Hook(request); err != nil {
				t.Fatalf("second Hook() error = %v", err)
			}
			if after := string(mustReadHookFile(t, surfacePath)); after != beforeSurface {
				t.Fatalf("second hook rewrote surface:\nbefore=%q\nafter=%q", beforeSurface, after)
			}
			if after := hookMapSnapshot(t, filepath.Join(organRoot, "maps")); after != beforeMaps {
				t.Fatalf("second hook changed maps:\nbefore=%q\nafter=%q", beforeMaps, after)
			}
		})
	}
}

func TestLegacySurfaceMigrationPreflightsGarbageBeforeWriting(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	surfacePath := filepath.Join(organRoot, "agents", "qa.md")
	body := lane.AgentSurfaceHeader + "\n" +
		"- 2026-08-01 Safe row -> This row must not partially migrate.\n" +
		"not a lane row\n"
	writeHookFile(t, surfacePath, body)
	hookGit(t, repository, "add", surfacePath)
	hookGit(t, repository, "commit", "-q", "-m", "malformed legacy lane surface")

	_, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "legacy lane surface") {
		t.Fatalf("garbage migration error = %v", err)
	}
	if got := string(mustReadHookFile(t, surfacePath)); got != body {
		t.Fatalf("garbage preflight rewrote surface: %q", got)
	}
	entries, readErr := os.ReadDir(filepath.Join(organRoot, "maps"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("garbage preflight created maps: entries=%v error=%v", entries, readErr)
	}
}

func TestLegacySurfaceMigrationRequiresTrackedSurfaceAtHEAD(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	surfacePath := filepath.Join(organRoot, "agents", "qa.md")
	body := lane.AgentSurfaceHeader + "\n" +
		"- 2026-08-01 Untracked note -> Never invent a provenance anchor.\n"
	writeHookFile(t, surfacePath, body)

	_, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not tracked at hook worktree HEAD") {
		t.Fatalf("untracked migration error = %v", err)
	}
	if got := string(mustReadHookFile(t, surfacePath)); got != body {
		t.Fatalf("untracked refusal rewrote surface: %q", got)
	}
	entries, readErr := os.ReadDir(filepath.Join(organRoot, "maps"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("untracked refusal created maps: entries=%v error=%v", entries, readErr)
	}
}

func TestLegacySurfaceMigrationHandlesMapSlugCollisions(t *testing.T) {
	for _, test := range []struct {
		name           string
		existingLesson string
		wantPointer    string
		wantNewMap     bool
	}{
		{
			name:           "reuse exact lesson",
			existingLesson: "Use one canonical cache key.",
			wantPointer:    "maps/cache-key.md",
		},
		{
			name:           "suffix different lesson",
			existingLesson: "This is a different lesson.",
			wantPointer:    "maps/cache-key-2.md",
			wantNewMap:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := hookRepository(t)
			organRoot := filepath.Join(repository, ".professor", "stm")
			anchorHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
			existingPath := filepath.Join(organRoot, "maps", "cache-key.md")
			writeHookFile(t, existingPath, legacyHookMap("Cache key", test.existingLesson, "anchor.txt:1", anchorHash))
			surfacePath := filepath.Join(organRoot, "agents", "qa.md")
			writeHookFile(t, surfacePath, lane.AgentSurfaceHeader+"\n- Cache key -> Use one canonical cache key.\n")
			hookGit(t, repository, "add", ".professor/stm")
			hookGit(t, repository, "commit", "-q", "-m", "collision fixture")
			beforeExisting := string(mustReadHookFile(t, existingPath))

			output, err := Hook(HookRequest{
				Kind:  HookCodexSubagentInject,
				Input: []byte(`{"agent_type":"qa","cwd":"` + repository + `"}`),
			})
			if err != nil {
				t.Fatalf("Hook() error = %v", err)
			}
			if !strings.Contains(string(output), "- Use one canonical cache key. -> "+test.wantPointer) {
				t.Fatalf("collision pointer = %s", output)
			}
			if after := string(mustReadHookFile(t, existingPath)); after != beforeExisting {
				t.Fatalf("collision rewrote existing map:\nbefore=%q\nafter=%q", beforeExisting, after)
			}
			newPath := filepath.Join(organRoot, "maps", "cache-key-2.md")
			_, statErr := os.Stat(newPath)
			if test.wantNewMap && statErr != nil {
				t.Fatalf("suffixed map missing: %v", statErr)
			}
			if !test.wantNewMap && !os.IsNotExist(statErr) {
				t.Fatalf("exact lesson created suffixed map: %v", statErr)
			}
		})
	}
}

func TestOrderedHookInputRejectsTrailingJSON(t *testing.T) {
	if _, err := parseOrderedObject([]byte(`{"tool_input":{}} {"extra":true}`)); err == nil {
		t.Fatal("ordered hook parser accepted a second JSON value")
	}
}

func TestHooksStayRepositoryHermeticAndStripWorktree(t *testing.T) {
	first := hookRepository(t)
	second := hookRepository(t)
	writeHookFile(t, filepath.Join(first, ".professor", "stm", "agents", "qa.md"), "- First -> maps/first.md\n")
	writeHookFile(t, filepath.Join(second, ".professor", "stm", "agents", "qa.md"), "- Second -> maps/second.md\n")
	worktree := filepath.Join(second, ".worktrees", "topic")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
		t.Fatal(err)
	}
	hookGit(t, second, "worktree", "add", "-q", "-b", "hook-topic", worktree)
	got, err := Hook(
		HookRequest{Kind: HookCodexSubagentInject, Input: []byte(`{"agent_type":"qa","cwd":"` + worktree + `"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Second ->") || strings.Contains(string(got), "First ->") {
		t.Fatalf("hermetic hook = %s", got)
	}
	claude, err := Hook(HookRequest{
		Kind: HookAgentInject, ProjectDirectory: worktree,
		Input: []byte(`{"tool_input":{"subagent_type":"qa","prompt":"go"}}`),
	})
	if err != nil || !strings.Contains(string(claude), "Second ->") || strings.Contains(string(claude), "First ->") {
		t.Fatalf("hermetic Claude hook = %s, %v", claude, err)
	}
}

func TestHooksAreLaneIsolatedAndUnsafeOrMissingLanesStaySilent(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	// Explore resolves onto the tracer lane, so its surface is agents/tracer.md.
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"), "- Explorer -> maps/explorer.md\n")
	writeHookFile(t, filepath.Join(organRoot, "agents", "qa.md"), "- QA -> maps/qa.md\n")
	for _, test := range []struct {
		name, agent, want string
		kind              HookKind
	}{
		{name: "Claude qa", agent: "qa", want: "QA ->", kind: HookAgentInject},
		{name: "Codex tracer", agent: "Explore", want: "Explorer ->", kind: HookCodexSubagentInject},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(`{"agent_type":"` + test.agent + `","cwd":"` + repository + `"}`)
			request := HookRequest{Kind: test.kind, Input: input}
			if test.kind == HookAgentInject {
				request.ProjectDirectory = repository
				request.Input = []byte(`{"tool_input":{"subagent_type":"` + test.agent + `","prompt":"go"}}`)
			}
			got, err := Hook(request)
			if err != nil || !strings.Contains(string(got), test.want) ||
				(test.want == "QA ->" && strings.Contains(string(got), "Explorer ->")) ||
				(test.want == "Explorer ->" && strings.Contains(string(got), "QA ->")) {
				t.Fatalf("lane hook = %s, %v", got, err)
			}
		})
	}
	for _, agentType := range []string{"general-purpose", "gitter", "../../qa"} {
		got, err := Hook(HookRequest{
			Kind:  HookCodexSubagentInject,
			Input: []byte(`{"agent_type":"` + agentType + `","cwd":"` + repository + `"}`),
		})
		if err != nil || len(got) != 0 {
			t.Fatalf("missing/unsafe lane %q = %q, %v", agentType, got, err)
		}
	}
}

func TestNudgePrefersOrganLocalFailureAndEmitsAtMostOneLine(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	marker := nightFailurePath(organRoot)
	writeHookFile(t, marker, "Phase: PREFLIGHT-FAILED\nReason: fixture\nPath: /fixture\n")
	writeHookFile(
		t,
		filepath.Join(organRoot, "dreamer", "2026-08-01.md"),
		"END-OF-SWEEP\nApplied: 2026-08-01T00:00:00Z\n",
	)
	got, err := Hook(
		HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "🌙 dreamer-night failed — inspect " + marker + "\n"
	if string(got) != want || strings.Count(string(got), "\n") != 1 {
		t.Fatalf("nudge = %q, want %q", got, want)
	}
}

func TestNudgeFailureBecomesPersistentEvidenceThenClears(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	badSweep := filepath.Join(organRoot, "dreamer", "2026-08-01.md")
	if err := os.MkdirAll(badSweep, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	got, err := Hook(HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: now})
	if err != nil || len(got) != 0 {
		t.Fatalf("transient failure = %q, %v", got, err)
	}
	marker := filepath.Join(organRoot, "tmp", "nudge.failed")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("persistent marker missing: %v", err)
	}
	if err := os.RemoveAll(badSweep); err != nil {
		t.Fatal(err)
	}
	got, err = Hook(HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: now})
	if err != nil || string(got) != "🌙 dreamer-night staleness check is broken — inspect "+marker+"\n" {
		t.Fatalf("persistent nudge = %q, %v", got, err)
	}
	got, err = Hook(HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: now})
	if err != nil || len(got) != 0 {
		t.Fatalf("clean run = %q, %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker survives clean run: %v", err)
	}
}

func TestNudgeFailureMarkerRefusesSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "outside")
	writeHookFile(t, target, "unchanged\n")
	marker := filepath.Join(directory, "nudge.failed")
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	err := writeNudgeFailure(marker, os.ErrInvalid, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink marker error = %v", err)
	}
	raw, readErr := os.ReadFile(target)
	if readErr != nil || string(raw) != "unchanged\n" {
		t.Fatalf("symlink target changed: %q, %v", raw, readErr)
	}
}

func TestMalformedNightFailureSurfacesPersistedNudgeFailureOnNextPrompt(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	nightMarker := nightFailurePath(organRoot)
	if err := os.MkdirAll(nightMarker, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	first, err := Hook(HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: now})
	if err != nil || len(first) != 0 {
		t.Fatalf("first malformed-marker nudge = %q, %v, want silent persistence", first, err)
	}
	nudgeMarker := filepath.Join(organRoot, "tmp", "nudge.failed")
	if _, err := os.Lstat(nudgeMarker); err != nil {
		t.Fatalf("malformed night marker did not persist nudge evidence: %v", err)
	}
	second, err := Hook(HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: now})
	want := "🌙 dreamer-night staleness check is broken — inspect " + nudgeMarker + "\n"
	if err != nil || string(second) != want || strings.Count(string(second), "\n") != 1 {
		t.Fatalf("second malformed-marker nudge = %q, %v, want %q", second, err, want)
	}
}

func TestNudgeStaleLineUsesNewestCompletedSweep(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(
		t,
		filepath.Join(organRoot, "dreamer", "2026-08-10.md"),
		"END-OF-SWEEP\nApplied: 2026-08-10T00:00:00Z\n",
	)
	writeHookFile(
		t,
		filepath.Join(organRoot, "dreamer", "2026-08-10-2.md"),
		"END-OF-SWEEP\nApplied: 2026-08-10T12:00:00Z\n",
	)
	got, err := Hook(
		HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: time.Date(2026, 8, 13, 12, 0, 1, 0, time.UTC)},
	)
	if err != nil || string(got) != "🌙 dreamer-night stale — newest applied sweep is 3d old; run /dreamer\n" {
		t.Fatalf("stale nudge = %q, %v", got, err)
	}
}

func TestNudgeIsSilentWithoutOrganAndForRecentHealthySweep(t *testing.T) {
	withoutOrgan := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(withoutOrgan, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := Hook(HookRequest{Kind: HookNudge, ProjectDirectory: withoutOrgan, Now: time.Now()})
	if err != nil || len(got) != 0 {
		t.Fatalf("nudge without organ = %q, %v", got, err)
	}
	repository := hookRepository(t)
	writeHookFile(
		t,
		filepath.Join(repository, ".professor", "stm", "dreamer", "2026-08-13.md"),
		"END-OF-SWEEP\nApplied: 2026-08-13T00:00:00Z\n",
	)
	got, err = Hook(
		HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)},
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("recent healthy nudge = %q, %v", got, err)
	}
}

func TestNudgeDoesNotDependOnGitBeingHealthy(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(
		t,
		filepath.Join(organRoot, "dreamer", "2026-08-01.md"),
		"END-OF-SWEEP\nApplied: 2026-08-01T00:00:00Z\n",
	)
	if err := os.Rename(filepath.Join(repository, ".git"), filepath.Join(repository, ".git.disabled")); err != nil {
		t.Fatal(err)
	}
	got, err := Hook(
		HookRequest{Kind: HookNudge, ProjectDirectory: repository, Now: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)},
	)
	if err != nil || string(got) != "🌙 dreamer-night stale — newest applied sweep is 12d old; run /dreamer\n" {
		t.Fatalf("nudge with broken Git = %q, %v", got, err)
	}
}

func hookRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	hookGit(t, repository, "init", "-q", "--initial-branch=main")
	hookGit(t, repository, "config", "user.name", "Hook Test")
	hookGit(t, repository, "config", "user.email", "hook@test.invalid")
	writeHookFile(t, filepath.Join(repository, "anchor.txt"), "anchor\n")
	writeHookFile(t, filepath.Join(repository, "stable.txt"), "stable\n")
	for _, directory := range []string{"maps", "archive", "dreamer", "agents"} {
		if err := os.MkdirAll(filepath.Join(repository, ".professor", "stm", directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeHookFile(t, filepath.Join(repository, ".professor", "stm", "stm.md"), "# fixture\n")
	// The lane declares the agent types it serves; Explore reads the tracer lane.
	writeHookFile(
		t,
		filepath.Join(repository, ".professor", "stm", "lanes", "tracer.md"),
		"Serves: tracer, Explore\n\nfixture profile\n",
	)
	hookGit(t, repository, "add", ".")
	hookGit(t, repository, "commit", "-q", "-m", "fixture")
	return repository
}

func hookGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeHookFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadHookFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertHookMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %#o, want %#o", path, got, want)
	}
}

func hookMapSnapshot(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		snapshot.WriteString(entry.Name())
		snapshot.WriteByte('\n')
		snapshot.Write(mustReadHookFile(t, filepath.Join(directory, entry.Name())))
	}
	return snapshot.String()
}

func legacyHookMap(title, lesson, anchor, blob string) string {
	return "# " + title + "\n\n## Lesson\n\n" + lesson +
		"\n\n## Anchors\n\n- `" + anchor + "` — blob `" + blob + "`\n"
}

func hookMap(firstHash, secondHash string) string {
	return "# Subject\n\n## Question\n\nWhat is the subject?\n\n## Answer\n\nIt is tested.\n\n## Derivation trail\n\nA fixture proves it.\n\nProvenance: 2026-08-13 · sid 0123abcd\n\n## Anchors\n\n- `anchor.txt` — blob `" + firstHash + "`\n- `stable.txt` — blob `" + secondHash + "`\n"
}

func TestCodexHookAnnotatesDriftOnHeaderCarryingSurface(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"),
		lane.AgentSurfaceHeader+"\n- Subject -> maps/subject.md\n")
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(t, filepath.Join(organRoot, "maps", "subject.md"), hookMap(firstHash, secondHash))
	writeHookFile(t, filepath.Join(repository, "anchor.txt"), "moved\n")
	hookGit(t, repository, "add", "anchor.txt")
	hookGit(t, repository, "commit", "-m", "move anchor")

	codex, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"tracer","cwd":"` + repository + `"}`),
	})
	if err != nil {
		t.Fatalf("Hook() error = %v", err)
	}
	if !strings.Contains(string(codex), `⚠ DRIFTED (1/2 anchors moved: anchor.txt)`) {
		t.Fatalf("Codex hook drops drift annotation on a header-carrying surface: %s", codex)
	}
	if strings.Contains(string(codex), "anchor-drift check FAILED") {
		t.Fatalf("healthy drift check must not report itself down: %s", codex)
	}
}

func TestHookDriftCheckDownRendersAsExplicitTrailerNotFreshness(t *testing.T) {
	repository := hookRepository(t)
	organRoot := filepath.Join(repository, ".professor", "stm")
	writeHookFile(t, filepath.Join(organRoot, "agents", "tracer.md"),
		lane.AgentSurfaceHeader+"\n- Subject -> maps/subject.md\n")
	// A malformed sibling map breaks the whole drift check (fail-closed). The
	// consumer must see the check-down trailer, never an unmarked surface that
	// reads as verified-fresh.
	firstHash := hookGit(t, repository, "rev-parse", "HEAD:anchor.txt")[:12]
	secondHash := hookGit(t, repository, "rev-parse", "HEAD:stable.txt")[:12]
	writeHookFile(t, filepath.Join(organRoot, "maps", "subject.md"), hookMap(firstHash, secondHash))
	writeHookFile(t, filepath.Join(organRoot, "maps", "broken.md"), "# Broken\n\n## Anchors\n\nnot an anchor row\n")

	codex, err := Hook(HookRequest{
		Kind:  HookCodexSubagentInject,
		Input: []byte(`{"agent_type":"tracer","cwd":"` + repository + `"}`),
	})
	if err != nil {
		t.Fatalf("Hook() error = %v", err)
	}
	if !strings.Contains(string(codex), "anchor-drift check FAILED") {
		t.Fatalf("drift-check failure rendered as absence: %s", codex)
	}
}
