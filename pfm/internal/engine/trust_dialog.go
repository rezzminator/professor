package engine

import "strings"

// Claude Code's folder-trust dialog ("Accessing workspace … Quick safety
// check") offers one confirm row and one cancel row.
const (
	claudeTrustConfirmRow  = "Yes, I trust this folder"
	claudeTrustExitRow     = "No, exit"
	claudeTrustContinueRow = "No, continue without these permissions"
)

// ClaudeTrustDialog reports whether a pane capture is Claude Code's folder-trust
// dialog. Its default row is "No, exit": Enter or Escape sent to it ends the
// chat, and the one key that would clear it makes Claude run that folder's
// project hooks and MCP servers — a decision for the human, never for pfm.
//
// Both halves are required. A composer whose prompt, or a transcript that
// quotes, one of the phrases is not the dialog; only the confirm row together
// with a cancel row (the exit row, or the continue-without-permissions variant)
// is.
func ClaudeTrustDialog(capture string) bool {
	if !strings.Contains(capture, claudeTrustConfirmRow) {
		return false
	}
	return strings.Contains(capture, claudeTrustExitRow) ||
		strings.Contains(capture, claudeTrustContinueRow)
}
