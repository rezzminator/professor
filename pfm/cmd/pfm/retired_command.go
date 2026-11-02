package main

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
	"github.com/rezzminator/professor/pfm/internal/update"
)

// answerRetiredCommand answers a retired `pfm {command}` from the clone's
// retired list alone, reading no machine config: one stderr line naming the
// successor and the port step, exit 2, nothing run. It reports false for a command the list
// does not retire, and for a list it could not read, named only when reportUnread (hooks stay quiet).
func answerRetiredCommand(command string, stderr io.Writer, reportUnread bool) (int, bool) {
	home, err := paths.Home()
	var lists retired.Lists
	if err == nil {
		lists, err = retired.LoadListsIfPresent(home)
	}
	if err != nil && reportUnread {
		fmt.Fprintf(stderr, "pfm: retired commands unread: %v\n", err)
	}
	name, successor, found := update.RetiredPfmCommand(lists.Names, command)
	if err != nil || !found {
		return 0, false
	}
	fmt.Fprintf(
		stderr,
		"pfm: %q is retired — run \"%s ...\"; port this project's files with \"pfm doctor --project-updates\"\n",
		name, successor,
	)
	return 2, true
}
