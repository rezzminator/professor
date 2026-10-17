package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// ownershipRetired is the retired-name set the ownership tests scan with: the
// installer cannot import the update package that derives the real one.
var ownershipRetired = RetiredRegistryNames{
	Agents:   map[string]bool{"rndier": true},
	Commands: map[string]bool{"wave": true, "qa/live": true},
	Skills:   map[string]bool{"wave-builder": true},
}

type ownershipHome struct{ home, store, clone string }

func newOwnershipHome(t *testing.T) ownershipHome {
	t.Helper()
	home := t.TempDir()
	return ownershipHome{home: home, store: ClaudeStore(home), clone: filepath.Join(home, ".professor")}
}

func (h ownershipHome) input(t *testing.T) RegistryScan {
	t.Helper()
	plan, err := PlanRegistryLinks(h.home, h.store, h.clone, "")
	if err != nil {
		t.Fatalf("plan registry links: %v", err)
	}
	return RegistryScan{
		Home: h.home, Store: h.store, Roots: []string{h.clone}, Plan: plan, Retired: ownershipRetired,
	}
}

// classes renders findings as "CLASS path-below-home", sorted.
func (h ownershipHome) classes(result RegistryOwnership) []string {
	rows := make([]string, 0, len(result.Findings))
	for _, finding := range result.Findings {
		relative, err := filepath.Rel(h.home, finding.Path)
		if err != nil {
			relative = finding.Path
		}
		rows = append(rows, finding.Class+" "+relative)
	}
	sort.Strings(rows)
	return rows
}

func TestInspectRegistryOwnershipClasses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, h ownershipHome)
		want  []string
		// failAt is the path a scan error must name, in the scan and in install's retirement.
		failAt func(h ownershipHome) string
	}{
		{
			name: "a dead owned link is DEAD in every registry, nested commands included",
			setup: func(t *testing.T, h ownershipHome) {
				managed := managedRootForHome(h.home)
				gone := filepath.Join(managed, "legacy", "group", "ls.command.md")
				symlinkFixture(t, gone, filepath.Join(h.store, "commands", "legacy", "group", "old"))
				symlinkFixture(t, filepath.Join(h.clone, "workflows", "deep-rr"), filepath.Join(h.store, "skills", "old"))
				symlinkFixture(t, gone, filepath.Join(h.home, ".agents", "skills", "old"))
			},
			want: []string{
				"DEAD .agents/skills/old", "DEAD .claude/commands/legacy/group/old", "DEAD .claude/skills/old",
			},
		},
		{
			name:   "an unreadable store is an error, never a shorter list",
			setup:  func(t *testing.T, h ownershipHome) { writeFixture(t, h.store, "not a directory") },
			failAt: func(h ownershipHome) string { return filepath.Join(h.store, "agents") },
		},
		{
			name: "a registry that is a file is an error",
			setup: func(t *testing.T, h ownershipHome) {
				writeFixture(t, filepath.Join(h.store, "commands"), "not a directory")
			},
			failAt: func(h ownershipHome) string { return filepath.Join(h.store, "commands") },
		},
		{
			name: "a target that cannot be inspected is an error",
			setup: func(t *testing.T, h ownershipHome) {
				target := filepath.Join(h.clone, "workflows", "deep-rr")
				writeFixture(t, filepath.Dir(target), "not a directory")
				symlinkFixture(t, target, filepath.Join(h.store, "commands", "old"))
			},
			failAt: func(h ownershipHome) string { return filepath.Join(h.clone, "workflows", "deep-rr") },
		},
		{
			name: "store links, present or gone",
			setup: func(t *testing.T, h ownershipHome) {
				stores := RetiredGeneratedStores(h.home)
				symlinkFixture(t, filepath.Join(stores[0], "a.md"), filepath.Join(h.store, "agents", "a.md"))
				writeFixture(t, filepath.Join(stores[1], "b.md"), "rendered")
				symlinkFixture(t, filepath.Join(stores[1], "b.md"), filepath.Join(h.store, "agents", "b.md"))
			},
			want: []string{"STORE .claude/agents/a.md", "STORE .claude/agents/b.md"},
		},
		{
			name: "an existing unplanned command is an orphan, a planned one is not",
			setup: func(t *testing.T, h ownershipHome) {
				commands := filepath.Join(h.clone, "templates", "global", "commands")
				writeFixture(t, filepath.Join(commands, "plan.md"), "planned")
				writeFixture(t, filepath.Join(commands, "flights", "x.md"), "planned dir")
				symlinkFixture(t, filepath.Join(commands, "plan.md"), filepath.Join(h.store, "commands", "plan.md"))
				symlinkFixture(t, filepath.Join(commands, "flights"), filepath.Join(h.store, "commands", "flights"))
				symlinkFixture(t, filepath.Join(commands, "plan.md"), filepath.Join(h.store, "commands", "other.md"))
			},
			want: []string{"ORPHAN .claude/commands/other.md"},
		},
		{
			name: "an agent is planned by its target's directory, not its name",
			setup: func(t *testing.T, h ownershipHome) {
				global := filepath.Join(h.clone, "templates", "global", "agents")
				generated := filepath.Join(h.clone, "templates", "generated", "agents")
				writeFixture(t, filepath.Join(global, "a.md"), "a")
				writeFixture(t, filepath.Join(generated, "b.md"), "b")
				writeFixture(t, filepath.Join(global, "sub", "c.md"), "shared file")
				symlinkFixture(t, filepath.Join(global, "a.md"), filepath.Join(h.store, "agents", "a.md"))
				symlinkFixture(t, filepath.Join(global, "a.md"), filepath.Join(h.store, "agents", "alias.md"))
				symlinkFixture(t, filepath.Join(generated, "b.md"), filepath.Join(h.store, "agents", "b.md"))
				symlinkFixture(t, filepath.Join(global, "sub", "c.md"), filepath.Join(h.store, "agents", "c.md"))
				symlinkFixture(t, filepath.Join(global, "gone.md"), filepath.Join(h.store, "agents", "d.md"))
			},
			want: []string{"DEAD .claude/agents/d.md", "ORPHAN .claude/agents/c.md"},
		},
		{
			name: "skills: template, fetched and embedded links are planned, the rest is orphaned",
			setup: func(t *testing.T, h ownershipHome) {
				skills := filepath.Join(h.clone, "templates", "global", "skills")
				managed := managedRootForHome(h.home)
				writeFixture(t, filepath.Join(skills, "tpl", "SKILL.md"), "tpl")
				writeFixture(t, filepath.Join(skills, "nomd", "notes.md"), "no SKILL.md: never linked")
				writeFixture(t, filepath.Join(skills, "sources.json"),
					`{"source_fetched":{"fetched":{"repo":"acme/fetched"}}}`)
				writeFixture(t, filepath.Join(managed, "skills", "fetched", "SKILL.md"), "fetched")
				writeFixture(t, filepath.Join(managed, "skills", "stale", "SKILL.md"), "no longer registered")
				writeFixture(t, filepath.Join(managed, "handoff.skill.md"), "embedded")
				symlinkFixture(t, filepath.Join(skills, "tpl"), filepath.Join(h.store, "skills", "tpl"))
				symlinkFixture(t, filepath.Join(skills, "nomd"), filepath.Join(h.store, "skills", "nomd"))
				symlinkFixture(t, filepath.Join(managed, "skills", "fetched"), filepath.Join(h.store, "skills", "fetched"))
				symlinkFixture(
					t, filepath.Join(managed, "skills", "fetched"), filepath.Join(h.home, ".agents", "skills", "fetched"),
				)
				symlinkFixture(
					t, filepath.Join(managed, "skills", "stale"), filepath.Join(h.home, ".agents", "skills", "stale"),
				)
				symlinkFixture(
					t, filepath.Join(managed, "handoff.skill.md"), filepath.Join(h.store, "skills", "handoff", "SKILL.md"),
				)
			},
			want: []string{"ORPHAN .agents/skills/stale", "ORPHAN .claude/skills/nomd"},
		},
		{
			name: "embedded command asset is planned",
			setup: func(t *testing.T, h ownershipHome) {
				managed := managedRootForHome(h.home)
				writeFixture(t, filepath.Join(managed, "reload.command.md"), "reload")
				symlinkFixture(t, filepath.Join(managed, "reload.command.md"), filepath.Join(h.store, "commands", "reload.md"))
			},
		},
		{
			name: "a regular skill directory is judged by its SKILL.md link",
			setup: func(t *testing.T, h ownershipHome) {
				old := filepath.Join(h.clone, "templates", "global", "skills", "old", "SKILL.md")
				writeFixture(t, old, "old")
				symlinkFixture(t, old, filepath.Join(h.store, "skills", "old", "SKILL.md"))
			},
			want: []string{"ORPHAN .claude/skills/old/SKILL.md"},
		},
		{
			name: "unmarked entries named for a retired name are RETIRED, once, never descended",
			setup: func(t *testing.T, h ownershipHome) {
				writeFixture(t, filepath.Join(h.store, "skills", "wave-builder", "SKILL.md"), "mine")
				writeFixture(t, filepath.Join(h.store, "skills", "ghostwriter", "SKILL.md"), "mine")
				writeFixture(t, filepath.Join(h.store, "agents", "rndier.md"), "mine")
				writeFixture(t, filepath.Join(h.store, "commands", "wave", "x.md"), "mine")
				symlinkFixture(t, filepath.Join(h.clone, "gone"), filepath.Join(h.store, "commands", "wave", "dead.md"))
				writeFixture(t, filepath.Join(h.store, "commands", "qa", "live.md"), "mine")
				writeFixture(t, filepath.Join(h.store, "commands", "qa", "keep.md"), "mine")
			},
			want: []string{
				"RETIRED .claude/agents/rndier.md",
				"RETIRED .claude/commands/qa/live.md",
				"RETIRED .claude/commands/wave",
				"RETIRED .claude/skills/wave-builder",
			},
		},
		{
			name: "a retired name that is a link is the user's",
			setup: func(t *testing.T, h ownershipHome) {
				symlinkFixture(t, filepath.Join(h.home, "foreign", "wave-builder"), filepath.Join(h.store, "skills", "wave-builder"))
			},
		},
		{
			name: "dot entries are skipped",
			setup: func(t *testing.T, h ownershipHome) {
				symlinkFixture(t, filepath.Join(h.clone, "gone"), filepath.Join(h.store, "skills", ".trash", "dead"))
				symlinkFixture(t, filepath.Join(h.clone, "gone"), filepath.Join(h.store, "commands", ".hidden"))
				symlinkFixture(t, filepath.Join(h.clone, "gone"), filepath.Join(h.store, "agents", ".x.md"))
			},
		},
		{
			name: "links outside the owned roots and plain files are never listed",
			setup: func(t *testing.T, h ownershipHome) {
				writeFixture(t, filepath.Join(h.home, "foreign", "f.md"), "foreign")
				symlinkFixture(t, filepath.Join(h.home, "foreign", "f.md"), filepath.Join(h.store, "agents", "f.md"))
				symlinkFixture(t, filepath.Join(h.home, "foreign", "g.md"), filepath.Join(h.store, "commands", "g.md"))
				symlinkFixture(t, h.clone+"-foreign", filepath.Join(h.store, "skills", "sibling"))
				writeFixture(t, filepath.Join(h.store, "commands", "h.md"), "mine")
				writeFixture(t, filepath.Join(h.clone, "RND", "mine.md"), "mine")
				symlinkFixture(t, filepath.Join(h.clone, "RND", "mine.md"), filepath.Join(h.store, "commands", "rnd.md"))
				symlinkFixture(t, filepath.Join(h.clone, "RND", "missing.md"), filepath.Join(h.store, "commands", "rnd-missing.md"))
			},
		},
		{
			name: "a registry root that is a dead link is DEAD, a live one is not an orphan",
			setup: func(t *testing.T, h ownershipHome) {
				symlinkFixture(t, filepath.Join(h.clone, "workflows", "retired-skills"), filepath.Join(h.home, ".agents", "skills"))
				writeFixture(t, filepath.Join(h.clone, "kept-commands", "x.md"), "x")
				symlinkFixture(t, filepath.Join(h.clone, "kept-commands"), filepath.Join(h.store, "commands"))
			},
			want: []string{"DEAD .agents/skills"},
		},
		{
			name: "a relative target is resolved against the link's directory",
			setup: func(t *testing.T, h ownershipHome) {
				writeFixture(t, filepath.Join(h.clone, "templates", "global", "commands", "rel.md"), "rel")
				symlinkFixture(
					t, "../../.professor/templates/global/commands/rel.md", filepath.Join(h.store, "commands", "rel.md"),
				)
				symlinkFixture(
					t, "../../.professor/templates/global/commands/dead.md", filepath.Join(h.store, "commands", "dead.md"),
				)
			},
			want: []string{"DEAD .claude/commands/dead.md"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			h := newOwnershipHome(t)
			test.setup(t, h)
			result, err := InspectRegistryOwnership(h.input(t))
			if test.failAt != nil {
				path := test.failAt(h)
				if err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("error=%v, want an error naming %s", err, path)
				}
				installer := &engine{options: Options{Home: h.home, ConfigDir: h.store}}
				if stepErr := installer.retireDeadRegistryLinks(); stepErr == nil ||
					!strings.Contains(stepErr.Error(), path) {
					t.Fatalf("install retirement error=%v, want an error naming %s", stepErr, path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := h.classes(result)
			want := append([]string(nil), test.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("findings=%q, want %q", got, want)
			}
		})
	}
}

// TestInspectRegistryOwnershipCounts: entries and registries the clean line prints.
func TestInspectRegistryOwnershipCounts(t *testing.T) {
	t.Parallel()
	h := newOwnershipHome(t)
	agents := filepath.Join(h.clone, "templates", "global", "agents", "a.md")
	writeFixture(t, agents, "a")
	symlinkFixture(t, agents, filepath.Join(h.store, "agents", "a.md"))
	writeFixture(t, filepath.Join(h.store, "commands", "mine.md"), "mine")
	writeFixture(t, filepath.Join(h.store, "skills", ".trash", "x"), "hidden")
	result, err := InspectRegistryOwnership(h.input(t))
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries != 2 || result.Registries != 3 || len(result.Findings) != 0 {
		t.Fatalf("entries=%d registries=%d findings=%+v, want 2 entries in 3 registries, none listed",
			result.Entries, result.Registries, result.Findings)
	}
}

// ownershipRowsFixture is spec row 33's Given.
func ownershipRowsFixture(t *testing.T) ownershipHome {
	t.Helper()
	h := newOwnershipHome(t)
	symlinkFixture(t,
		filepath.Join(RetiredGeneratedStores(h.home)[0], "astra.md"), filepath.Join(h.store, "agents", "astra.md"))
	oldSkill := filepath.Join(h.clone, "templates", "global", "skills", "old-skill")
	if err := os.MkdirAll(oldSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkFixture(t, oldSkill, filepath.Join(h.store, "skills", "old-skill"))
	symlinkFixture(
		t,
		filepath.Join(
			h.clone,
			"templates",
			"global",
			"commands",
			"gone.md",
		),
		filepath.Join(h.store, "commands", "gone.md"),
	)
	writeFixture(t, filepath.Join(h.store, "skills", "wave-builder", "SKILL.md"), "wave-builder body")
	writeFixture(t, filepath.Join(h.store, "skills", "ghostwriter", "SKILL.md"), "ghostwriter body")
	writeFixture(t, filepath.Join(h.store, "skills", ".trash", "old", "SKILL.md"), "trash body")
	return h
}

func registryRows(output string) []string {
	var rows []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "doctor: registry") {
			rows = append(rows, line)
		}
	}
	return rows
}

func reportRegistries(h ownershipHome) (rows []string, warnings, failures int) {
	var output bytes.Buffer
	warnings, failures = ReportGlobalRegistries(
		&output, h.home, true,
		&paths.MapEnv{Values: map[string]string{enginesync.EnvBin: testjail.EngineSyncBin()}},
		nil, nil, ownershipRetired,
	)
	return registryRows(output.String()), warnings, failures
}

func TestRegistryOwnershipDoctorRows(t *testing.T) {
	t.Parallel()
	t.Run("row 33 fixture lists one row per class", func(t *testing.T) {
		t.Parallel()
		h := ownershipRowsFixture(t)
		rows, warnings, failures := reportRegistries(h)
		store := RetiredGeneratedStores(h.home)[0]
		want := []string{
			"doctor: registry STORE " + filepath.Join(h.store, "agents", "astra.md") + " -> " +
				filepath.Join(store, "astra.md") + " — retired store; run pfm install --yes",
			"doctor: registry DEAD " + filepath.Join(h.store, "commands", "gone.md") + " -> " +
				filepath.Join(h.clone, "templates", "global", "commands", "gone.md") +
				" — target gone; run pfm install --yes",
			"doctor: registry ORPHAN " + filepath.Join(h.store, "skills", "old-skill") + " -> " +
				filepath.Join(h.clone, "templates", "global", "skills", "old-skill") +
				" — Professor no longer ships it; run pfm install --yes",
			"doctor: registry RETIRED " + filepath.Join(h.store, "skills", "wave-builder") +
				` — unmarked; Professor retired "wave-builder"; left in place`,
		}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
		}
		if warnings != 2 || failures != 2 {
			t.Fatalf(
				"warnings=%d failures=%d, want 2 and 2 (STORE and ORPHAN fail; DEAD and RETIRED warn)",
				warnings,
				failures,
			)
		}
	})
	t.Run("only planned links print the ok line with counts", func(t *testing.T) {
		t.Parallel()
		h := newOwnershipHome(t)
		agent := filepath.Join(h.clone, "templates", "global", "agents", "a.md")
		command := filepath.Join(h.clone, "templates", "global", "commands", "c.md")
		skill := filepath.Join(h.clone, "templates", "global", "skills", "s")
		writeFixture(t, agent, "a")
		writeFixture(t, command, "c")
		writeFixture(t, filepath.Join(skill, "SKILL.md"), "s")
		symlinkFixture(t, agent, filepath.Join(h.store, "agents", "a.md"))
		symlinkFixture(t, command, filepath.Join(h.store, "commands", "c.md"))
		symlinkFixture(t, skill, filepath.Join(h.store, "skills", "s"))
		rows, warnings, failures := reportRegistries(h)
		want := []string{"doctor: registry-ownership ok (3 entries in 3 registries)"}
		if !reflect.DeepEqual(rows, want) || warnings != 0 || failures != 0 {
			t.Fatalf("rows=%q warnings=%d failures=%d, want %q and none", rows, warnings, failures, want)
		}
	})
	t.Run("a walk error prints the failure line and no ok line", func(t *testing.T) {
		t.Parallel()
		h := newOwnershipHome(t)
		writeFixture(t, filepath.Join(h.store, "agents"), "not a directory")
		rows, _, failures := reportRegistries(h)
		if failures != 1 || len(rows) != 1 ||
			!strings.HasPrefix(rows[0], "doctor: registry-ownership check failed: ") ||
			!strings.Contains(rows[0], filepath.Join(h.store, "agents")) {
			t.Fatalf("failures=%d rows=%q", failures, rows)
		}
	})
}

func TestRetireRegistryOwnership(t *testing.T) {
	t.Parallel()
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply=%t", apply), func(t *testing.T) {
			t.Parallel()
			h := ownershipRowsFixture(t)
			mine := map[string]string{}
			for _, path := range []string{
				filepath.Join(h.store, "skills", "wave-builder", "SKILL.md"),
				filepath.Join(h.store, "skills", "ghostwriter", "SKILL.md"),
				filepath.Join(h.store, "skills", ".trash", "old", "SKILL.md"),
			} {
				mine[path] = readFixture(t, path)
			}
			var output bytes.Buffer
			installer := &engine{
				options:     Options{Home: h.home, ConfigDir: h.store, Stdout: &output},
				managedRoot: managedRootForHome(h.home),
				apply:       apply,
			}
			if err := installer.retireDeadRegistryLinks(); err != nil {
				t.Fatal(err)
			}
			retired := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
			sort.Strings(retired)
			want := []string{
				"  change  retire " + filepath.Join(h.store, "agents", "astra.md") + " (retired store link -> " +
					filepath.Join(RetiredGeneratedStores(h.home)[0], "astra.md") + ")",
				"  change  retire " + filepath.Join(h.store, "commands", "gone.md") + " (dead pfm link -> " +
					filepath.Join(h.clone, "templates", "global", "commands", "gone.md") + ")",
				"  change  retire " + filepath.Join(h.store, "skills", "old-skill") + " (orphan pfm link -> " +
					filepath.Join(h.clone, "templates", "global", "skills", "old-skill") + ")",
			}
			sort.Strings(want)
			if !reflect.DeepEqual(retired, want) {
				t.Fatalf("transcript:\n%s\nwant:\n%s", strings.Join(retired, "\n"), strings.Join(want, "\n"))
			}
			for path, content := range mine {
				if readFixture(t, path) != content {
					t.Errorf("%s changed", path)
				}
			}
			if !apply {
				return
			}
			rows, warnings, failures := reportRegistries(h)
			wantRow := "doctor: registry RETIRED " + filepath.Join(h.store, "skills", "wave-builder") +
				` — unmarked; Professor retired "wave-builder"; left in place`
			if !reflect.DeepEqual(rows, []string{wantRow}) || warnings != 1 || failures != 0 {
				t.Fatalf(
					"after install: rows=%q warnings=%d failures=%d, want %q alone",
					rows,
					warnings,
					failures,
					wantRow,
				)
			}
		})
	}
}
