package installer

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// engineSyncPathEngine is an installer over a home whose engine-sync stable
// link exists (stable true) or does not.
func engineSyncPathEngine(t *testing.T, mode Mode, stable bool) (*engine, *bytes.Buffer, string, string) {
	t.Helper()
	t.Setenv(enginesync.EnvHome, "")
	t.Setenv("XDG_DATA_HOME", "")
	installer, output, home := engineSyncGlobalEngine(t, mode, nil)
	stablePath := enginesync.StablePath(installer.env(), home)
	if stable {
		writeFixture(t, stablePath, "binary")
	}
	return installer, output, home, stablePath
}

func TestWireEngineSyncPathLink(t *testing.T) {
	t.Run("links the stable binary onto PATH, then reports it ok", func(t *testing.T) {
		installer, output, home, stable := engineSyncPathEngine(t, ModeApply, true)
		link := engineSyncPathLink(home)
		if err := installer.wireEngineSyncPathLink(); err != nil {
			t.Fatal(err)
		}
		if got, err := os.Readlink(link); err != nil || got != stable {
			t.Fatalf("%s -> %q (%v), want %s", link, got, err, stable)
		}
		if !strings.Contains(output.String(), "  change  link "+link+" -> "+stable+"\n") {
			t.Fatalf("the link was not reported:\n%s", output.String())
		}
		output.Reset()
		if err := installer.wireEngineSyncPathLink(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "  ok      "+link+"\n") || strings.Contains(output.String(), "change") {
			t.Fatalf("pfm's own link was rewritten:\n%s", output.String())
		}
	})
	t.Run("a dry run names the link and writes none", func(t *testing.T) {
		installer, output, home, _ := engineSyncPathEngine(t, ModeDryRun, true)
		if err := installer.wireEngineSyncPathLink(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(engineSyncPathLink(home)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the preview wrote the link: %v", err)
		}
		if !strings.Contains(output.String(), "  change  link ") {
			t.Fatalf("the preview did not name the link:\n%s", output.String())
		}
	})
	for _, kind := range []string{"foreign link", "regular file"} {
		t.Run("leaves a "+kind, func(t *testing.T) {
			installer, output, home, _ := engineSyncPathEngine(t, ModeApply, true)
			link := engineSyncPathLink(home)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if kind == "regular file" {
				writeFixture(t, link, "operator's own")
			} else if err := os.Symlink(filepath.Join(home, "elsewhere"), link); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(link)
			if err := installer.wireEngineSyncPathLink(); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(link)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("%s was replaced: %v", link, err)
			}
			if want := "  skip    " + link + " is not pfm's link — left unchanged\n"; !strings.Contains(
				output.String(),
				want,
			) {
				t.Fatalf("transcript lacks %q:\n%s", want, output.String())
			}
		})
	}
	t.Run("skips while the stable binary is absent", func(t *testing.T) {
		installer, output, home, stable := engineSyncPathEngine(t, ModeApply, false)
		if err := installer.wireEngineSyncPathLink(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(engineSyncPathLink(home)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a link to a missing binary was written: %v", err)
		}
		if want := "  skip    engine-sync PATH link not written: " + stable + " is absent"; !strings.Contains(
			output.String(),
			want,
		) {
			t.Fatalf("transcript lacks %q:\n%s", want, output.String())
		}
	})
}

func TestUnwireEngineSyncPathLinkRemovesOnlyPfmsLink(t *testing.T) {
	installer, _, home, stable := engineSyncPathEngine(t, ModeUninstall, true)
	link := engineSyncPathLink(home)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, "elsewhere")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if err := installer.unwireEngineSyncPathLink(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(link); err != nil || got != foreign {
		t.Fatalf("uninstall touched the foreign link: %q (%v)", got, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stable, link); err != nil {
		t.Fatal(err)
	}
	if err := installer.unwireEngineSyncPathLink(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstall left pfm's link: %v", err)
	}
}

// pfm install links engine-sync onto PATH once the plugin wrapper has made
// the stable binary, and pfm uninstall removes that link.
func TestInstallAndUninstallEngineSyncPathLink(t *testing.T) {
	t.Setenv(enginesync.EnvHome, "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	stable := enginesync.StablePath(paths.OSEnv{}, home)
	if err := os.MkdirAll(filepath.Dir(stable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(os.Getenv(enginesync.EnvBin), stable); err != nil {
		t.Fatal(err)
	}
	link := engineSyncPathLink(home)
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		var output bytes.Buffer
		if _, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t), Mode: mode, Home: home, Stdout: &output, Runner: &fakeRunner{},
		}); err != nil {
			t.Fatalf("mode %d: %v\n%s", mode, err, output.String())
		}
		_, err := os.Lstat(link)
		if (mode == ModeApply) == errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("mode %d: %s present=%v (%v)", mode, link, err == nil, err)
		}
		if mode == ModeApply {
			if got, err := os.Readlink(link); err != nil || got != stable {
				t.Fatalf("%s -> %q (%v), want %s", link, got, err, stable)
			}
		}
	}
}

func TestReportEngineSyncHost(t *testing.T) {
	ownedFile := `{"version":1,"machine":{"managedBy":"professor"}}`
	for _, testCase := range []struct {
		name         string
		setup        func(t *testing.T, home, stable string)
		want         string
		wantWarnings int
		wantFailures int
	}{
		{name: "nothing on the host", setup: func(*testing.T, string, string) {}},
		{
			name: "pfm's file and a resolving pfm link",
			setup: func(t *testing.T, home, stable string) {
				writeFixture(t, enginesync.GlobalConfigPath(home), ownedFile)
				writeFixture(t, stable, "binary")
				linkEngineSync(t, home, stable)
			},
		},
		{
			name: "a global file that is not pfm's",
			setup: func(t *testing.T, home, _ string) {
				writeFixture(t, enginesync.GlobalConfigPath(home), `{"version":1}`)
			},
			want:         "doctor: ~/.claude/engine-sync.json is not managed by professor — pfm left it unchanged\n",
			wantWarnings: 1,
		},
		{
			name: "a dangling PATH link",
			setup: func(t *testing.T, home, stable string) {
				linkEngineSync(t, home, stable)
			},
			want: "doctor: engine-sync PATH link ~/.local/bin/engine-sync -> " +
				"~/.local/share/engine-sync/bin/engine-sync is dangling: the engine-sync binary is gone — " +
				engineSyncMissingRepair + "\n",
			wantFailures: 1,
		},
		{
			name: "a dangling foreign link",
			setup: func(t *testing.T, home, _ string) {
				linkEngineSync(t, home, filepath.Join(home, "other-engine-sync"))
			},
			want: "doctor: engine-sync PATH link ~/.local/bin/engine-sync -> ~/other-engine-sync " +
				"is dangling and not pfm's link — remove or repoint it\n",
			wantFailures: 1,
		},
		{
			name: "pfm's link read under another data home",
			setup: func(t *testing.T, home, stable string) {
				writeFixture(t, stable, "binary")
				linkEngineSync(t, home, stable)
				t.Setenv("XDG_DATA_HOME", filepath.Join(home, "other-data"))
			},
		},
		{
			name: "a foreign file on PATH",
			setup: func(t *testing.T, home, _ string) {
				writeFixture(t, engineSyncPathLink(home), "operator's own")
			},
			want:         "doctor: ~/.local/bin/engine-sync is not pfm's link — left unchanged\n",
			wantWarnings: 1,
		},
		{
			name: "a resolving foreign link",
			setup: func(t *testing.T, home, _ string) {
				other := filepath.Join(home, "other-engine-sync")
				writeFixture(t, other, "binary")
				if err := os.MkdirAll(filepath.Dir(engineSyncPathLink(home)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, engineSyncPathLink(home)); err != nil {
					t.Fatal(err)
				}
			},
			want:         "doctor: ~/.local/bin/engine-sync is not pfm's link — left unchanged\n",
			wantWarnings: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(enginesync.EnvHome, "")
			t.Setenv("XDG_DATA_HOME", "")
			home := t.TempDir()
			testCase.setup(t, home, enginesync.StablePath(paths.OSEnv{}, home))
			var output bytes.Buffer
			warnings, failures := ReportEngineSyncHost(&output, home, paths.OSEnv{})
			if output.String() != testCase.want || warnings != testCase.wantWarnings ||
				failures != testCase.wantFailures {
				t.Fatalf("report = %q warnings=%d failures=%d, want %q warnings=%d failures=%d",
					output.String(), warnings, failures, testCase.want, testCase.wantWarnings, testCase.wantFailures)
			}
		})
	}
}

func linkEngineSync(t *testing.T, home, stable string) {
	t.Helper()
	link := engineSyncPathLink(home)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stable, link); err != nil {
		t.Fatal(err)
	}
}
