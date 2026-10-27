package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRetireDeadRegistryLinks(t *testing.T) {
	t.Parallel()
	for _, apply := range []bool{false, true} {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("apply=%t/keep=%t", apply, keep), func(t *testing.T) {
				home := t.TempDir()
				store, repo := ClaudeStore(home), filepath.Join(home, ".professor")
				group := filepath.Join(store, "commands", "legacy", "group")
				link := filepath.Join(group, "ls.md")
				target := filepath.Join(managedRootForHome(home), "legacy", "group", "ls.command.md")
				if err := os.MkdirAll(group, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if keep {
					writeFixture(t, filepath.Join(group, "operator.md"), "keep")
				}
				live := filepath.Join(repo, "templates", "global", "commands", "live.md")
				writeFixture(t, live, "live")
				assertRoot := filepath.Join(store, "commands")
				if err := os.Symlink(live, filepath.Join(assertRoot, "live.md")); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				installer := &engine{
					options:     Options{Home: home, ConfigDir: store, Stdout: &output},
					managedRoot: managedRootForHome(home),
					apply:       apply,
				}
				if err := installer.retireDeadRegistryLinks(); err != nil {
					t.Fatal(err)
				}
				want := "  change  retire " + link + " (dead pfm link -> " + target + ")\n"
				if output.String() != want {
					t.Fatalf("transcript=%q, want %q", output.String(), want)
				}
				_, err := os.Lstat(link)
				if apply && !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("dead link survived: %v", err)
				}
				if !apply && err != nil {
					t.Fatalf("preview changed link: %v", err)
				}
				for _, dir := range []string{group, filepath.Dir(group)} {
					_, err := os.Stat(dir)
					if apply && !keep && !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("empty directory survived %s: %v", dir, err)
					}
					if (!apply || keep) && err != nil {
						t.Fatalf("directory removed %s: %v", dir, err)
					}
				}
				if _, err := os.Stat(filepath.Join(assertRoot, "live.md")); err != nil {
					t.Fatal(err)
				}
				if keep && readFixture(t, filepath.Join(group, "operator.md")) != "keep" {
					t.Fatal("operator file changed")
				}
			})
		}
	}
}

func TestRegistryDeadLinksUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	link := filepath.Join(ClaudeStore(home), "commands", "old.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managedRootForHome(home), "gone.md"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(
		context.Background(),
		Options{Mode: ModeUninstall, Home: home, Runner: &fakeRunner{}, MCPConfigPath: testConfigPath(t)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstall left dead link: %v", err)
	}
}

// TestRegistryOwnershipUninstall: an uninstall plans nothing, so every owned
// link left in a registry goes — a live legacy-workflows link and one into the
// retired store included — while a foreign link stays.
func TestRegistryOwnershipUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store := ClaudeStore(home)
	clone := filepath.Join(home, ".professor")
	live := filepath.Join(clone, "workflows", "left.md")
	writeFixture(t, live, "exists")
	retiredStore := filepath.Join(RetiredGeneratedStores(home)[1], "role.md")
	writeFixture(t, retiredStore, "rendered")
	foreign := filepath.Join(home, "foreign", "mine.md")
	writeFixture(t, foreign, "mine")
	owned := []string{
		filepath.Join(store, "commands", "left.md"), filepath.Join(store, "agents", "role.md"),
	}
	symlinkFixture(t, live, owned[0])
	symlinkFixture(t, retiredStore, owned[1])
	mine := filepath.Join(store, "commands", "mine.md")
	symlinkFixture(t, foreign, mine)
	if _, err := Run(
		context.Background(),
		Options{Mode: ModeUninstall, Home: home, Runner: &fakeRunner{}, MCPConfigPath: testConfigPath(t)},
	); err != nil {
		t.Fatal(err)
	}
	for _, link := range owned {
		if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("uninstall left the owned link %s: %v", link, err)
		}
	}
	assertLink(t, mine, foreign)
}

func TestRegistryDeadLinkResilience(t *testing.T) {
	for _, scenario := range []string{"unusable marker", "busy store", "lock error", "unreadable skill sources"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			managed := managedRootForHome(home)
			mode := ModeUninstall
			link := filepath.Join(home, ".claude", "commands", "gone.md")
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(managed, "gone.md"), link); err != nil {
				t.Fatal(err)
			}
			var want string
			switch scenario {
			case "unusable marker":
				writeFixture(t, paths.SourceRepoPath(home), filepath.Join(home, "moved-away")+"\n")
				_, markerErr := paths.ReadSourceRepoMarker(home)
				want = "  skip    registry-ownership check skipped: " +
					"read source repository marker for registry-ownership check: " + markerErr.Error() +
					" — rerun pfm install --yes from inside your Professor clone\n"
			case "busy store":
				if err := os.MkdirAll(managed, 0o700); err != nil {
					t.Fatal(err)
				}
				holder, err := os.Open(managed)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := holder.Close(); err != nil {
						t.Error(err)
					}
				})
				if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				want = "  skip    registry-ownership check skipped: another pfm install holds " +
					skillStoreRoot(home) + "\n"
			case "lock error":
				if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(managed, managed); err != nil {
					t.Fatal(err)
				}
				_, openErr := os.Open(managed)
				want = "  skip    registry-ownership check skipped: open skill store lock " +
					managed + ": " + openErr.Error() + "\n"
			case "unreadable skill sources":
				// An install with a malformed sources.json cannot tell a fetched skill's link
				// from an orphan: it retires nothing, the live fetched link included.
				mode = ModeApply
				clone := filepath.Join(home, ".professor")
				writeFixture(t, filepath.Join(clone, "templates", "global", "skills", "sources.json"), "{not json")
				fetched := filepath.Join(managed, "skills", "fetched")
				writeFixture(t, filepath.Join(fetched, "SKILL.md"), "fetched")
				agentsLink := filepath.Join(home, ".agents", "skills", "fetched")
				symlinkFixture(t, fetched, agentsLink)
				t.Cleanup(func() { assertLink(t, agentsLink, fetched) })
				_, _, sourcesErr := loadSkillSources(clone, "")
				want = "  skip    registry-ownership check skipped: " + errPlanSkillSources.Error() + ": " +
					sourcesErr.Error() + "\n"
			}
			var output bytes.Buffer
			installer := &engine{
				options: Options{
					Mode:      mode,
					Home:      home,
					ConfigDir: filepath.Join(home, ".claude"),
					Stdout:    &output,
				},
				apply:       true,
				managedRoot: managed,
			}
			if err := installer.retireDeadRegistryLinks(); err != nil {
				t.Fatalf("registry retirement: %v", err)
			}
			if output.String() != want {
				t.Fatalf("output=%q, want %q", output.String(), want)
			}
			assertLink(t, link, filepath.Join(managed, "gone.md"))
		})
	}
}

func TestRegistryDeadLinkLockReleased(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	installer := &engine{
		options: Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		apply:   true, managedRoot: managed,
	}
	if err := installer.retireDeadRegistryLinks(); err != nil {
		t.Fatal(err)
	}
	unlock, busy, err := installer.lockSkillStore(skillStoreRoot(home))
	if err != nil || busy {
		t.Fatalf("lock after retirement: busy=%t err=%v", busy, err)
	}
	unlock()
}

// TestRetireDeadRegistryRootLink pins a registry root that is itself a dead
// pfm link — ~/.agents/skills once linked whole into a clone path since
// removed. Install and preview retire it and stop: the empty-directory climb
// never walks above the registry it started in.
func TestRetireDeadRegistryRootLink(t *testing.T) {
	t.Parallel()
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprint(apply), func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			root := filepath.Join(home, ".agents", "skills")
			target := filepath.Join(home, ".professor", "workflows", "retired-skills")
			if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			installer := &engine{
				options:     Options{Home: home, ConfigDir: store, Stdout: &output},
				managedRoot: managedRootForHome(home),
				apply:       apply,
			}
			done := make(chan error, 1)
			go func() { done <- installer.retireDeadRegistryLinks() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("retireDeadRegistryLinks never returned on a dead registry root link")
			}
			want := "  change  retire " + root + " (dead pfm link -> " + target + ")\n"
			if output.String() != want {
				t.Fatalf("transcript=%q, want %q", output.String(), want)
			}
			if _, err := os.Stat(filepath.Dir(root)); err != nil {
				t.Fatalf("directory above the registry removed: %v", err)
			}
		})
	}
}

// A crash between SymlinkOverDir's swap and its cleanup leaves the displaced
// copy at {registry}/.pfm-link-XXXX. Install removes a stage in a registry it
// manages once it is older than linkStageMinAge, through its own transcript,
// and leaves a younger one (a swap in flight) and every non-stage entry alone.
func TestRetireStaleLinkStages(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply=%t", apply), func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			now := time.Now()
			old, young := now.Add(-linkStageMinAge-time.Minute), now.Add(-linkStageMinAge/2)
			age := func(path string, at time.Time) string {
				t.Helper()
				if err := os.Chtimes(path, at, at); err != nil {
					t.Fatal(err)
				}
				return path
			}
			staleDir := filepath.Join(store, "skills", ".pfm-link-1234")
			writeFixture(t, filepath.Join(staleDir, "SKILL.md"), "displaced copy")
			stale := []string{
				staleDir,
				filepath.Join(store, "commands", "group", ".pfm-link-5678"),
				filepath.Join(store, "agents", ".pfm-link-9abc"),
				filepath.Join(home, ".agents", "skills", ".pfm-link-def0"),
			}
			for _, path := range stale[1:] {
				writeFixture(t, path, "staged")
			}
			keep := []string{
				filepath.Join(store, "skills", ".pfm-link-fresh"),
				filepath.Join(store, "skills", "demo", "SKILL.md"),
				filepath.Join(store, "commands", "pfm-link-notastage.md"),
			}
			for _, path := range keep {
				writeFixture(t, path, "keep")
			}
			for _, path := range stale {
				age(path, old)
			}
			age(keep[0], young)
			age(keep[1], old)
			age(keep[2], old)

			var output bytes.Buffer
			installer := &engine{
				options: Options{
					Home: home, ConfigDir: store, Stdout: &output, Now: func() time.Time { return now },
				},
				managedRoot: managedRootForHome(home),
				apply:       apply,
			}
			if err := installer.retireStaleLinkStages(); err != nil {
				t.Fatal(err)
			}
			for _, path := range stale {
				line := "  change  remove stale link stage " + path
				if !strings.Contains(output.String(), line) {
					t.Errorf("transcript lacks %q:\n%s", line, output.String())
				}
				_, err := os.Lstat(path)
				if apply != errors.Is(err, fs.ErrNotExist) {
					t.Errorf("stage %s after apply=%t: %v", path, apply, err)
				}
			}
			for _, path := range keep {
				if _, err := os.Lstat(path); err != nil {
					t.Errorf("%s was removed: %v", path, err)
				}
				if strings.Contains(output.String(), path) {
					t.Errorf("transcript names the kept %s:\n%s", path, output.String())
				}
			}
			if installer.report.Changed != len(stale) {
				t.Fatalf("changed=%d, want %d:\n%s", installer.report.Changed, len(stale), output.String())
			}
		})
	}
}

// TestRetireDeadRulesLink: a rules link into the clone whose rule is gone is
// retired as a dead pfm link; a live rule the plan writes stays.
func TestRetireDeadRulesLink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	store, repo := ClaudeStore(home), filepath.Join(home, ".professor")
	live := filepath.Join(repo, "templates", "global", "rules", "live.md")
	gone := filepath.Join(repo, "templates", "global", "rules", "gone.md")
	writeFixture(t, live, "live")
	symlinkFixture(t, live, filepath.Join(store, "rules", "live.md"))
	dead := filepath.Join(store, "rules", "gone.md")
	symlinkFixture(t, gone, dead)
	var output bytes.Buffer
	installer := &engine{
		options:     Options{Home: home, ConfigDir: store, Stdout: &output},
		managedRoot: managedRootForHome(home),
		apply:       true,
	}
	if err := installer.retireDeadRegistryLinks(); err != nil {
		t.Fatal(err)
	}
	want := "  change  retire " + dead + " (dead pfm link -> " + gone + ")\n"
	if output.String() != want {
		t.Fatalf("transcript=%q, want %q", output.String(), want)
	}
	if _, err := os.Lstat(dead); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dead rules link survived: %v", err)
	}
	assertLink(t, filepath.Join(store, "rules", "live.md"), live)
}
