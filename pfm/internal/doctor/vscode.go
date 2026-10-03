package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

const missingState = "missing"

// printVSCodeDoctor answers issue #24 9b: no doctor row covered any of the VS
// Code wiring, so a link that a product's own index never registered
// (9a) read as a clean install with nothing to grep for. It runs only when
// the ownership ledger exists — a host that never ran `pfm install --vscode`
// prints one line and no warning, an absence never worth flagging — and
// otherwise reports one row per recorded extension link target (link state
// AND the product's own index state, which the link alone cannot answer)
// plus one row per owned settings file. installer.InspectVSCode is the same
// reader `pfm install --vscode` itself uses, so doctor can never assert a
// state the installer did not derive the same way. Every warning row carries
// a warningID and prints through filter, so doctor.ignoreWarnings can
// silence it (warning_ids.go).
func printVSCodeDoctor(stdout io.Writer, home, primaryDir string, filter warningFilter) int {
	const installFix, vscodeFix = "run pfm install --yes", "run pfm install --yes --vscode"
	report, err := installer.InspectVSCode(home)
	if err != nil {
		return filter.warn(
			stdout,
			warnVSCodeInspect,
			fmt.Sprintf("doctor: vscode unreadable error=%v", err),
			installFix,
		)
	}
	if !report.Managed {
		fmt.Fprintln(stdout, "doctor: vscode not managed (pfm install --vscode never ran)")
		return 0
	}

	warnings := 0
	for _, product := range report.Products {
		row := "doctor: vscode product=" + product.Root
		switch product.LinkState {
		case "ok":
			switch product.IndexState {
			case "registered":
				fmt.Fprintf(stdout, "%s link=ok index=registered version=%s\n", row, product.Version)
			case missingState:
				warnings += filter.warn(stdout, warnVSCodeIndex, row+" link=ok index=MISSING", installFix)
			case unreadableState:
				warnings += filter.warn(
					stdout,
					warnVSCodeIndex,
					fmt.Sprintf("%s link=ok index=UNREADABLE error=%s", row, product.IndexError),
					"",
				)
			default:
				// A state InspectVSCode did not derive is "we failed to
				// look", never a silent clean row.
				warnings += filter.warn(
					stdout,
					warnVSCodeIndex,
					fmt.Sprintf("%s link=ok index=UNKNOWN(%s)", row, product.IndexState),
					"",
				)
			}
		case brokenState:
			warnings += filter.warn(
				stdout,
				warnVSCodeLink,
				fmt.Sprintf("%s link=BROKEN(%s)", row, product.LinkTarget),
				installFix,
			)
		case missingState:
			warnings += filter.warn(stdout, warnVSCodeLink, row+" link=MISSING", installFix)
		default:
			warnings += filter.warn(
				stdout,
				warnVSCodeLink,
				fmt.Sprintf("%s link=UNKNOWN(%s)", row, product.LinkState),
				"",
			)
		}
	}
	for _, settings := range report.Settings {
		row := fmt.Sprintf(
			"doctor: vscode settings=%s profile=PFM(%s) default=%s",
			settings.Path,
			settings.Profile,
			settings.Default,
		)
		if settings.Profile == missingState || settings.Profile == unreadableState {
			warnings += filter.warn(stdout, warnVSCodeSettings, row, "")
		} else {
			fmt.Fprintln(stdout, row)
		}
		if settings.Error == "" && primaryDir != "" {
			envFix := vscodeFix
			if settings.ProfileConflict {
				// `pfm install --vscode` refuses an operator's own PFM profile,
				// so the fix names the step that clears the refusal first.
				envFix = fmt.Sprintf(
					`rename or remove the "PFM" terminal profile in %s, then %s`,
					settings.Path,
					vscodeFix,
				)
			}
			switch {
			case settings.ClaudeConfigDir == "":
				warnings += filter.warn(
					stdout,
					warnVSCodeSettings,
					"doctor: vscode settings="+settings.Path+" CLAUDE_CONFIG_DIR missing",
					envFix,
				)
			case settings.ClaudeConfigDir != primaryDir:
				warnings += filter.warn(
					stdout,
					warnVSCodeSettings,
					fmt.Sprintf(
						"doctor: vscode settings=%s CLAUDE_CONFIG_DIR=%s, want %s",
						settings.Path,
						settings.ClaudeConfigDir,
						primaryDir,
					),
					envFix,
				)
			}
		}
		if settings.Error != "" {
			fmt.Fprintf(stdout, "doctor: vscode settings=%s error=%s\n", settings.Path, settings.Error)
		}
	}
	return warnings
}
