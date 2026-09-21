package picker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/ui"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

const professorLatestReleaseURL = "https://github.com/" + updatecheck.ProfessorRepo + "/releases/latest"

var startProfessorUpdateCheck = func(ctx context.Context, argv []string, options deps.StartOptions) error {
	_, err := obs.Runner(deps.RealRunner{}).Start(ctx, argv, options)
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

// cachedProfessorUpdateFailureRow is cachedProfessorUpdateRow's failure twin:
// where that one turns a successful check that found a release into its own
// row, this turns a detached checker that has been failing on every attempt
// into one — the state professorUpdateCheckNotice could previously only
// report as a stderr line printed before the interactive picker takes the
// terminal, gone the instant the alt screen opens. hasUpdate wins outright: a
// successful check that DID find a release makes any earlier failure history
// moot, exactly as professorUpdateCheckNotice already treats it.
func cachedProfessorUpdateFailureRow(runtime pfmconfig.Runtime, hasUpdate bool) (compose.Row, bool) {
	if !runtime.IsRelease() || hasUpdate {
		return compose.Row{}, false
	}
	cachePath := professorUpdateCachePath(runtime)
	marker, failing, err := updatecheck.ReadFailure(cachePath)
	if err != nil || !failing {
		return compose.Row{}, false
	}
	repo, err := installer.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		return compose.Row{}, false
	}
	return compose.Row{
		Kind: compose.ProfessorUpdateFailed,
		ID:   "pfm-update-check-failed-" + marker.At.UTC().Format(time.RFC3339),
		Name: fmt.Sprintf(
			"failing since %s (%s): %s",
			marker.At.Local().Format("2006-01-02 15:04"),
			marker.Class,
			marker.Reason,
		),
		Project: filepath.Base(repo),
		CWD:     repo,
	}, true
}

// professorUpdateCheckNotice names an update-check state cachedProfessorUpdateRow
// itself cannot show: it only ever answers ("", false) for three very
// different situations — a genuine "no update available" (updatecheck.Read
// found nothing AND the detached checker has been succeeding), the cache
// file itself being unreadable, and a detached checker that has been failing
// every run for days while Read keeps answering found=false. Folding all
// three into silence is exactly "an error rendering as absence"; this
// returns a one-line stderr notice for the second and third, "" for the
// first (and whenever the release gate does not apply).
//
// It is safe to call and print BEFORE the interactive picker takes the
// terminal (mirrors the flag-validation stderr writes already at the top of
// Run) — never mid-frame, which is why triggerProfessorUpdateCheck's own
// detached child stays silent instead of writing here itself.
func professorUpdateCheckNotice(runtime pfmconfig.Runtime) string {
	if !runtime.IsRelease() {
		return ""
	}
	cachePath := professorUpdateCachePath(runtime)
	_, found, err := updatecheck.Read(cachePath, runtime.Version)
	if err != nil {
		return fmt.Sprintf("pfm ls: could not read the Professor update cache: %v", err)
	}
	if found {
		// An update IS available — cachedProfessorUpdateRow already renders
		// it as its own picker row; a second notice would only repeat it.
		return ""
	}
	marker, failing, err := updatecheck.ReadFailure(cachePath)
	if err != nil {
		return fmt.Sprintf("pfm ls: could not read the Professor update-check failure marker: %v", err)
	}
	if !failing {
		return ""
	}
	return fmt.Sprintf(
		"pfm ls: Professor update check failing since %s (%s): %s",
		marker.At.Local().Format("2006-01-02 15:04"),
		marker.Class,
		marker.Reason,
	)
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
