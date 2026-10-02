package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInspectDeadRegistryLinks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, registry, source                                  string
		live, file, absent, broken, relative, sibling, rootFile bool
		want                                                    int
	}{
		{name: "dead command", registry: "commands", source: "repo", want: 1},
		{name: "nested managed command", registry: "commands/chat/group", source: "managed", want: 1},
		{name: "retired skill", registry: "skills", source: "repo", want: 1},
		{name: "dead agent", registry: "agents", source: "generated", want: 1},
		{name: "live pfm link", registry: "commands", source: "repo", live: true},
		{name: "foreign link", registry: "commands", source: "foreign"},
		{name: "real file", registry: "commands", file: true},
		{name: "agents skills", registry: "external", source: "managed", want: 1},
		{name: "absent root", absent: true},
		{name: "unreadable registry", broken: true},
		{name: "registry file", registry: "commands", broken: true, rootFile: true},
		{name: "relative target", registry: "commands", source: "repo", relative: true, want: 1},
		{name: "root prefix sibling", registry: "skills", source: "repo", sibling: true},
		{name: "target stat error", registry: "commands", source: "repo", broken: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			store, repo := ClaudeStore(home), filepath.Join(home, ".professor")
			managed := managedRootForHome(home)
			var link, target string
			switch {
			case test.rootFile:
				writeFixture(t, filepath.Join(store, test.registry), "not a directory")
			case test.broken && test.registry == "":
				writeFixture(t, store, "not a directory")
			case !test.absent:
				root := filepath.Join(store, test.registry)
				if test.registry == "external" {
					root = filepath.Join(home, ".agents", "skills")
				}
				link = filepath.Join(root, "old")
				switch test.source {
				case "repo":
					target = filepath.Join(repo, "workflows", "deep-rr")
				case "managed":
					target = filepath.Join(managed, "chat", "group", "ls.command.md")
				case "generated":
					target = filepath.Join(paths.GeneratedClaudeAgentsDir(home), "old.md")
				default:
					target = filepath.Join(home, "foreign", "old")
				}
				if test.sibling {
					target = filepath.Join(repo+"-foreign", "old")
				}
				if test.live {
					writeFixture(t, target, "live")
				}
				if test.broken {
					writeFixture(t, filepath.Dir(target), "not a directory")
				}
				if test.file {
					writeFixture(t, link, "operator file")
				} else {
					if err := os.MkdirAll(root, 0o700); err != nil {
						t.Fatal(err)
					}
					wire := target
					if test.relative {
						var err error
						wire, err = filepath.Rel(root, target)
						if err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(wire, link); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := InspectDeadRegistryLinks(home, store, []string{repo})
			if test.broken {
				path := filepath.Join(store, "agents")
				if test.rootFile {
					path = filepath.Join(store, test.registry)
				} else if target != "" {
					path = target
				}
				if err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("error=%v, want error naming %s", err, path)
				}
				installer := &engine{options: Options{Home: home, ConfigDir: store}}
				if stepErr := installer.retireDeadRegistryLinks(); stepErr == nil ||
					!strings.Contains(stepErr.Error(), path) {
					t.Fatalf("install retirement error=%v, want %s", stepErr, path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != test.want {
				t.Fatalf("dead links=%+v, want %d", got, test.want)
			}
			if test.want == 1 && !reflect.DeepEqual(got, []DeadLink{{Path: link, Target: target}}) {
				t.Fatalf("dead links=%+v, want %s -> %s", got, link, target)
			}
		})
	}
}

func TestRetireDeadRegistryLinks(t *testing.T) {
	t.Parallel()
	for _, apply := range []bool{false, true} {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("apply=%t/keep=%t", apply, keep), func(t *testing.T) {
				home := t.TempDir()
				store, repo := ClaudeStore(home), filepath.Join(home, ".professor")
				group := filepath.Join(store, "commands", "chat", "group")
				link := filepath.Join(group, "ls.md")
				target := filepath.Join(managedRootForHome(home), "chat", "group", "ls.command.md")
				if err := os.MkdirAll(group, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if keep {
					writeFixture(t, filepath.Join(group, "operator.md"), "keep")
				}
				live := filepath.Join(repo, "live.md")
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

func TestRegistryDeadLinkDoctorRows(t *testing.T) {
	t.Parallel()
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprintf("broken=%t", broken), func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			link := filepath.Join(store, "commands", "old.md")
			target := filepath.Join(home, ".professor", "gone.md")
			if broken {
				writeFixture(t, store, "not a directory")
			} else {
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			warnings, failures := ReportGlobalRegistries(&output, home, true, &paths.MapEnv{})
			var rows []string
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.HasPrefix(line, "doctor: registry ") {
					rows = append(rows, line)
				}
			}
			if broken {
				if failures != 1 || len(rows) != 1 ||
					!strings.HasPrefix(rows[0], "doctor: registry dead-link check failed: ") ||
					!strings.Contains(rows[0], filepath.Join(store, "agents")) {
					t.Fatalf("failures=%d rows=%q", failures, rows)
				}
			} else {
				want := "doctor: registry dead link " + link + " -> " + target + " — run pfm install --yes"
				if warnings != 1 || failures != 0 || !reflect.DeepEqual(rows, []string{want}) {
					t.Fatalf("warnings=%d failures=%d rows=%q, want %q", warnings, failures, rows, want)
				}
			}
		})
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

func TestRegistryInstallKeepsManagedLeftovers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	managed := managedRootForHome(home)
	fixtures := []struct{ path, content string }{
		{"chat/stray-note.md", "operator chat note\n"},
		{"codex-skills/bb/keepme.txt", "operator skill note\n"},
		{"chat/ls.command.md", "legacy command card\n"},
	}
	for _, fixture := range fixtures {
		writeFixture(t, filepath.Join(managed, filepath.FromSlash(fixture.path)), fixture.content)
	}
	if _, err := Run(
		context.Background(),
		Options{
			Mode:          ModeApply,
			Home:          home,
			Runner:        &fakeRunner{},
			MCPConfigPath: testConfigPath(t),
			CodexHomes:    []string{},
		},
	); err != nil {
		t.Fatalf("install over managed leftovers: %v", err)
	}
	for _, fixture := range fixtures {
		path := filepath.Join(managed, filepath.FromSlash(fixture.path))
		if got := readFixture(t, path); got != fixture.content {
			t.Errorf("%s=%q, want %q", fixture.path, got, fixture.content)
		}
	}
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
			target := filepath.Join(home, ".professor", "retired-skills")
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
