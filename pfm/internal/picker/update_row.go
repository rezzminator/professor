package picker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/ui"
	"hostops/pfm/internal/updatecheck"
)

const professorLatestReleaseURL = "https://github.com/" + updatecheck.ProfessorRepo + "/releases/latest"

var startProfessorUpdateCheck = func(ctx context.Context, argv []string, options deps.StartOptions) error {
	_, err := (deps.RealRunner{}).Start(ctx, argv, options)
	return err
}

func professorUpdateCachePath(runtime pfmconfig.Runtime) string {
	return filepath.Join(filepath.Dir(runtime.Paths.DB), "update-check.json")
}

func cachedProfessorUpdateRow(runtime pfmconfig.Runtime) (compose.Row, bool) {
	if !runtime.IsRelease() {
		return compose.Row{}, false
	}
	notice, found, err := updatecheck.Read(professorUpdateCachePath(runtime), runtime.Version)
	if err != nil || !found {
		return compose.Row{}, false
	}
	repo, err := installer.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		return compose.Row{}, false
	}
	return compose.Row{
		Kind:    compose.ProfessorUpdate,
		ID:      "pfm-update-" + notice.Latest,
		Name:    notice.Latest,
		Project: filepath.Base(repo),
		CWD:     repo,
	}, true
}

// triggerProfessorUpdateCheck is intentionally fire-and-forget and silent.
// The detached child owns network and cache errors so the picker frame is not corrupted.
func triggerProfessorUpdateCheck(runtime pfmconfig.Runtime) {
	if !runtime.IsRelease() {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	latestURL := professorLatestReleaseURL
	if override := strings.TrimSpace((paths.OSEnv{}).Get("PFM_UPDATE_LATEST_URL")); override != "" {
		latestURL = override
	}
	argv := []string{
		executable,
		"internal",
		"update-check",
		"--cache",
		professorUpdateCachePath(runtime),
		"--current",
		runtime.Version,
		"--url",
		latestURL,
	}
	_ = startProfessorUpdateCheck(context.Background(), argv, deps.StartOptions{
		Detach: true,
	})
}

func openProfessorUpdate(
	ctx context.Context,
	outcome ui.Outcome,
	stdout, stderr io.Writer,
	runtime pfmconfig.Runtime,
) int {
	row := outcome.Row
	switch outcome.Engine {
	case pfmengine.Claude:
		row.Kind = compose.NewClaude
	case pfmengine.Codex:
		row.Kind = compose.NewCodex
	case pfmengine.OpenCode:
		row.Kind = compose.NewOpenCode
	default:
		fmt.Fprintf(stderr, "pfm ls: update: unsupported engine %q\n", outcome.Engine)
		return 1
	}
	row.Name = "Professor update " + strings.TrimPrefix(outcome.Row.ID, "pfm-update-")
	row.Account = outcome.PrimaryAccount
	return pfmchat.OpenRow(
		ctx,
		row,
		outcome.PrimaryAccount,
		false,
		professorUpdatePrompt(outcome.Row),
		stdout,
		stderr,
		&runtime,
	)
}

func professorUpdatePrompt(row compose.Row) string {
	target := strings.TrimPrefix(row.ID, "pfm-update-")
	return "Professor " + target + " is available. Work only in this Professor source clone. " +
		"First run `pfm version` for the installed version and `git fetch --tags origin`, then read EVERY release-notes file after the installed version through " + target + ", oldest first, with `git show " + target + ":releases/vX.Y.Z.md` (`git show " + target + ":CHANGELOG.md` lists them) — skipping a release skips its migration actions. " +
		"Merge their `#### → For:` actions into one checklist, a later release superseding an earlier one on the same surface, and mark each as before or after the update. " +
		"Then present a concise overview of every change and migration impact, with that checklist. " +
		"Ask the user for explicit approval before making any change. Only after approval, do the before-update actions, run `pfm update --to " + target + "`, work the rest of the checklist, then run `pfm doctor` and report the exact result of each step. " +
		"Do not push, tag, publish, release, or edit the source manually."
}
