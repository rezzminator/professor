package installer

import "github.com/rezzminator/professor/pfm/internal/retired"

// retiredLists is the retired lists of the clone this run installs from
// (globalSourceRepoRoot), read once per run; options.retired stands in for
// the file in tests. A failed read is every caller's error: install defers it
// once, and a step needing a list does nothing without one.
func (installer *engine) retiredLists() (retired.Lists, error) {
	if installer.options.retired != nil {
		return *installer.options.retired, nil
	}
	if !installer.retiredRead {
		installer.retiredRead = true
		repo, err := installer.globalSourceRepoRoot()
		if err == nil {
			installer.retired, err = retired.ReadLists(repo)
		}
		installer.retiredErr = err
	}
	return installer.retired, installer.retiredErr
}
