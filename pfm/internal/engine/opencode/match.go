package opencode

import pfmengine "hostops/pfm/internal/engine"

type Matcher struct{}

func (Matcher) IsCommand(argv []string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.OpenCode, argv, false, binaries...)
}
