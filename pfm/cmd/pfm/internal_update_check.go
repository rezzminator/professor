package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/ui"
	"hostops/pfm/internal/updatecheck"
)

const professorLatestReleaseURL = "https://github.com/" + updatecheck.ProfessorRepo + "/releases/latest"

var startProfessorUpdateCheck = func(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func professorUpdateCachePath(runtime commandRuntime) string {
	return filepath.Join(filepath.Dir(runtime.Paths.DB), "update-check.json")
}

func cachedProfessorUpdateRow(runtime commandRuntime) (compose.Row, bool) {
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
// The child owns every network and cache error; the picker neither waits for
// it nor emits a warning that could corrupt the active alternate-screen frame.
func triggerProfessorUpdateCheck(runtime commandRuntime) {
	if !runtime.IsRelease() {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer func() {
		if err := null.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "pfm update check: close null device: %v\n", err)
		}
	}()
	latestURL := professorLatestReleaseURL
	if override := strings.TrimSpace(os.Getenv("PFM_UPDATE_LATEST_URL")); override != "" {
		latestURL = override
	}
	command := exec.Command(
		executable,
		internalCommand, "update-check",
		"--cache", professorUpdateCachePath(runtime),
		"--current", runtime.Version,
		"--url", latestURL,
	)
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = startProfessorUpdateCheck(command)
}

func runInternalUpdateCheck(args []string, stderr io.Writer) int {
	flags := cli.NewFlagSet(
		"internal update-check",
		"usage: pfm internal update-check --cache PATH --current vX.Y.Z --url URL",
		stderr,
	)
	cache := flags.String("cache", "", "update cache path")
	current := flags.String("current", "", "installed pfm version")
	latestURL := flags.String("url", professorLatestReleaseURL, "latest release redirect")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *cache == "" || *current == "" || *latestURL == "" {
		flags.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 12 * time.Second}
	if err := updatecheck.CheckForUpdate(ctx, *cache, *current, *latestURL, client); err != nil {
		fmt.Fprintf(stderr, "pfm internal update-check: %v\n", err)
		return 1
	}
	return 0
}

func openProfessorUpdate(
	ctx context.Context,
	outcome ui.Outcome,
	stdout, stderr io.Writer,
	runtime commandRuntime,
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
	return openRowWithPrompt(
		ctx,
		row,
		outcome.PrimaryAccount,
		false,
		professorUpdatePrompt(outcome.Row),
		stdout,
		stderr,
		runtime,
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
