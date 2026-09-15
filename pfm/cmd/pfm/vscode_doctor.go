package main

import (
	"fmt"
	"io"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
)

// printVSCodeDoctor answers issue #24 9b: no doctor row covered any of the VS
// Code wiring, so a link that a product's own index never registered
// (9a) read as a clean install with nothing to grep for. It runs only when
// the ownership ledger exists — a host that never ran `pfm install --vscode`
// prints one line and no warning, an absence never worth flagging — and
// otherwise reports one row per recorded extension link target (link state
// AND the product's own index state, which the link alone cannot answer)
// plus one row per owned settings file. installer.InspectVSCode is the same
// reader `pfm install --vscode` itself uses, so doctor can never assert a
// state the installer did not derive the same way.
func printVSCodeDoctor(stdout io.Writer, home string, _ config.Config) int {
	report, err := installer.InspectVSCode(home)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: vscode unreadable error=%v — run pfm install --yes\n", err)
		return 1
	}
	if !report.Managed {
		fmt.Fprintln(stdout, "doctor: vscode not managed (pfm install --vscode never ran)")
		return 0
	}

	warnings := 0
	for _, product := range report.Products {
		switch product.LinkState {
		case "ok":
			switch product.IndexState {
			case "registered":
				fmt.Fprintf(
					stdout,
					"doctor: vscode product=%s link=ok index=registered version=%s\n",
					product.Root,
					product.Version,
				)
			case "missing":
				warnings++
				fmt.Fprintf(
					stdout,
					"doctor: vscode product=%s link=ok index=MISSING — run pfm install --yes\n",
					product.Root,
				)
			case "unreadable":
				warnings++
				fmt.Fprintf(
					stdout,
					"doctor: vscode product=%s link=ok index=UNREADABLE error=%s\n",
					product.Root,
					product.IndexError,
				)
			default:
				// A state InspectVSCode did not derive is "we failed to
				// look", never a silent clean row.
				warnings++
				fmt.Fprintf(
					stdout,
					"doctor: vscode product=%s link=ok index=UNKNOWN(%s)\n",
					product.Root,
					product.IndexState,
				)
			}
		case "broken":
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: vscode product=%s link=BROKEN(%s) — run pfm install --yes\n",
				product.Root,
				product.LinkTarget,
			)
		case "missing":
			warnings++
			fmt.Fprintf(stdout, "doctor: vscode product=%s link=MISSING — run pfm install --yes\n", product.Root)
		default:
			warnings++
			fmt.Fprintf(stdout, "doctor: vscode product=%s link=UNKNOWN(%s)\n", product.Root, product.LinkState)
		}
	}
	for _, settings := range report.Settings {
		fmt.Fprintf(
			stdout,
			"doctor: vscode settings=%s profile=PFM(%s) default=%s\n",
			settings.Path,
			settings.Profile,
			settings.Default,
		)
		if settings.Error != "" {
			fmt.Fprintf(stdout, "doctor: vscode settings=%s error=%s\n", settings.Path, settings.Error)
		}
		if settings.Profile == "missing" || settings.Profile == "unreadable" {
			warnings++
		}
	}
	return warnings
}
