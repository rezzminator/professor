//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleasedUpdaterInstallGate(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	repo := sharedSourceRepo(t)
	h := &e2eHarness{t: t, repo: repo, goCache: requiredGoEnv(t, "GOCACHE"), goModCache: requiredGoEnv(t, "GOMODCACHE")}
	bin := os.Getenv(e2eScriptBinaryEnv)
	if bin == "" {
		t.Fatalf("%s: e2e binary was not built", e2eScriptBinaryEnv)
	}
	const updaterLine = "  refuse  updater — this install migrates the host layout, and the pfm update running it " +
		"predates the install journal"
	for _, tc := range []struct {
		name           string
		explicitConfig bool
		marked         bool
	}{
		{name: "v0.76-v0.77", explicitConfig: true},
		{name: "v0.78"},
		{name: "journal-aware", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := h.newHome(bin)
			run := func(binary string, extraEnv []string, args ...string) commandResult {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
				defer cancel()
				command := exec.CommandContext(ctx, binary, args...)
				command.Dir = repo
				command.Env = append(h.environment(home), extraEnv...)
				command.Env = append(command.Env,
					"PFM_MANAGED_SETTINGS_DIR="+filepath.Join(home, "managed-settings.d"),
					"PFM_LOG_LEVEL=off")
				output, err := command.CombinedOutput()
				return commandResult{stdout: string(output), err: err}
			}
			h.requireSuccess("stage installed assets", run(filepath.Join(home, e2eCanonicalPFM), nil,
				"install", "--yes", "--skip-harvest", "--skip-themes"))
			plantLegacyHostLayout(t, home, repo)
			stage := filepath.Join(home, ".local", "share", "pfm", "update-e2e", "pfm-a")
			info, err := os.Stat(filepath.Join(home, e2eCanonicalPFM))
			if err != nil {
				t.Fatal(err)
			}
			if err := copyFile(filepath.Join(home, e2eCanonicalPFM), stage, info.Mode().Perm()); err != nil {
				t.Fatal(err)
			}
			journalRoot := filepath.Join(home, ".local", "state", "pfm", "migrations")
			if err := os.RemoveAll(journalRoot); err != nil {
				t.Fatal(err)
			}
			before := hostLayoutSnapshot(t, home)
			args := []string{"install", "--yes", "--skip-harvest", "--skip-themes"}
			if tc.explicitConfig {
				args = append([]string{"--config", filepath.Join(home, ".config", "pfm", "pfm.config.json")}, args...)
			} else {
				configPath := filepath.Join(home, "pfm.config.json")
				if _, err := os.Stat(configPath); err == nil {
					args = append([]string{"--config", configPath}, args...)
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			extraEnv := []string{"PFM_SOURCE_REPO=" + repo}
			if tc.marked {
				extraEnv = append(extraEnv, "PFM_UPDATE_INSTALL=1")
			}
			result := run(stage, extraEnv, args...)
			if tc.marked {
				if strings.Contains(result.stdout, updaterLine) {
					t.Fatalf("marked install refused as an old updater:\n%s", result.stdout)
				}
				h.requireSuccess("marked updater install", result)
				return
			}
			if result.err == nil || !strings.Contains(result.stdout, updaterLine) ||
				!strings.Contains(result.stdout, "make -C "+repo+"/pfm host-install") {
				t.Fatalf("released updater got err=%v, output:\n%s", result.err, result.stdout)
			}
			hostLayoutSameSnapshot(t, "released updater refusal", before, hostLayoutSnapshot(t, home))
			if _, err := os.Stat(journalRoot); !os.IsNotExist(err) {
				t.Fatalf("refusal created migration journal: %v", err)
			}
		})
	}
}
