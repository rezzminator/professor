package doctor

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func printClaudeStoreChecks(stdout io.Writer, runtime config.Runtime) int {
	store := installer.ClaudeStore(runtime.Paths.Home)
	report := installer.InspectClaudeStore(store, runtime.Config.Accounts)
	failures := 0
	clean := true
	for _, entry := range report.Entries {
		switch entry.State {
		case missingState:
			fmt.Fprintf(stdout, "store: %s missing — run pfm install\n", entry.Path)
			failures++
		case "broken":
			fmt.Fprintf(
				stdout,
				"store: %s broken: %v — remove %s, then run pfm install --yes\n",
				entry.Path,
				entry.Err,
				entry.Path,
			)
			failures++
		case unreadableState:
			fmt.Fprintf(stdout, "store: %s UNREADABLE error=%v\n", entry.Path, entry.Err)
			failures++
		}
	}
	for _, account := range report.Accounts {
		switch account.State {
		case missingState:
			fmt.Fprintf(stdout, "account: %d %s missing — run pfm install\n", account.ID, account.Dir)
			failures++
			continue
		case unreadableState:
			fmt.Fprintf(stdout, "account: %d %s UNREADABLE error=%v\n", account.ID, account.Dir, account.Err)
			failures++
			continue
		case "not-dir":
			fmt.Fprintf(
				stdout,
				"account: %d %s is a file, not a directory — mv %s %s.bak, then run pfm install\n",
				account.ID,
				account.Dir,
				account.Dir,
				account.Dir,
			)
			failures++
			continue
		case "store":
			clean = false
			continue
		}
		for _, link := range account.Links {
			switch link.State {
			case missingState:
				fmt.Fprintf(stdout, "account-link: %s missing — run pfm install\n", link.Path)
				failures++
			case "foreign":
				fmt.Fprintf(
					stdout,
					"account-link: %s points at %s outside the store — run pfm install --yes; its host check names the merge\n",
					link.Path,
					link.Target,
				)
				clean = false
			case "elsewhere":
				fmt.Fprintf(
					stdout,
					"account-link: %s points at %s, want %s — run pfm install\n",
					link.Path,
					link.Target,
					filepath.Join(store, link.Entry),
				)
				failures++
			case unreadableState:
				fmt.Fprintf(stdout, "account-link: %s UNREADABLE error=%v\n", link.Path, link.Err)
				failures++
			case "real":
				clean = false
			}
		}
	}
	if failures == 0 && clean {
		fmt.Fprintf(
			stdout,
			"account-links: ok (%d accounts × %d entries)\n",
			len(report.Accounts),
			len(installer.StoreEntries),
		)
	}
	return failures
}
