package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/retired"
)

// loadRetiredLists reads the clone's retired lists for every doctor check
// that needs one; a list that cannot load is one FAIL row, and those checks
// then run on empty lists.
func loadRetiredLists(stdout io.Writer, home string) (retired.Lists, int) {
	lists, err := retired.LoadLists(home)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: retired_lists FAIL %v — restore %s in the clone\n", err, retired.FileRel)
		return retired.Lists{}, 1
	}
	return lists, 0
}
