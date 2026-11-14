package picker

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func init() {
	fleetindex.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	fleetindex.RegisterSource(pfmengine.Codex, codexengine.Source{})
	fleetindex.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	gather.RegisterMatcher(pfmengine.OpenCode, opencodeengine.Matcher{})
}

func jailTest(t *testing.T) string {
	t.Helper()
	return testjail.InstalledHome(t)
}
