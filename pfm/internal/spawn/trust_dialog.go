package spawn

import "fmt"

// TrustRefusal is the one refusal line for a chat held at Claude Code's
// folder-trust dialog, whichever door met it. The dialog's default row is
// "No, exit", and trusting a folder makes Claude run that folder's project
// hooks and MCP servers — a decision pfm leaves to the human. directory may be
// empty when the caller no longer knows it.
func (result Result) TrustRefusal(name, directory string) string {
	folder := ""
	if directory != "" {
		folder = " for " + directory
	}
	return fmt.Sprintf(
		"%s is held at Claude Code's folder-trust dialog%s — pfm pressed nothing "+
			"(Enter there selects \"No, exit\"); trust the folder yourself: "+
			"tmux -L %s attach -t %s, choose \"Yes, I trust this folder\"",
		name, folder, result.Socket, result.Session,
	)
}
