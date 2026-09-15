package config

import (
	"fmt"
	"io"
	"os"
)

// ReportRoots prints doctor's per-engine account-root reachability line for
// every configured Claude and Codex account, and returns the warnings they
// earned. When claudeAbsent, Claude roots are never stat'd — the installer
// never wires an account with no Claude Code binary, so an unreachable root
// there is not a defect either — and are named skipped instead, excluded
// from the reachable/total tally rather than falsely counted healthy. Codex
// roots are always checked, regardless of Claude's presence.
func ReportRoots(w io.Writer, accounts []Account, codexAccounts []CodexAccount, claudeAbsent bool) int {
	warnings := 0
	total, reachable := 0, 0
	for _, account := range accounts {
		if claudeAbsent {
			fmt.Fprintf(
				w,
				"doctor: roots claude root=%s skipped (no Claude Code binary installed)\n",
				account.ProjectDir,
			)
			continue
		}
		total++
		if info, err := os.Stat(account.ProjectDir); err == nil && info.IsDir() {
			reachable++
		} else {
			warnings++
			fmt.Fprintf(w, "doctor: warning unreachable_root=%s\n", account.ProjectDir)
		}
	}
	for _, account := range codexAccounts {
		total++
		if info, err := os.Stat(account.Home); err == nil && info.IsDir() {
			reachable++
		} else {
			warnings++
			fmt.Fprintf(w, "doctor: warning unreachable_root=%s\n", account.Home)
		}
	}
	fmt.Fprintf(w, "doctor: roots reachable=%d total=%d\n", reachable, total)
	return warnings
}
