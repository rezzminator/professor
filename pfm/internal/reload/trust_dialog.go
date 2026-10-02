package reload

import "fmt"

// trustDialogError is the refusal for a reborn pane held at Claude Code's
// folder-trust dialog: its default row is "No, exit", so an Enter would exit the
// chat, and trusting a folder is the human's decision.
func trustDialogError(pane string) error {
	return fmt.Errorf(
		"reload --then: pane %s is held at Claude Code's folder-trust dialog; "+
			"pfm pressed nothing (Enter there selects \"No, exit\"); "+
			"attach it and choose \"Yes, I trust this folder\"",
		pane,
	)
}
