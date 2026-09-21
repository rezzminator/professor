package codex

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

type Matcher struct{}

func (Matcher) IsCommand(argv []string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.Codex, argv, false, binaries...)
}
