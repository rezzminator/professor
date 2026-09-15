package reap

import (
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	opencodeengine "hostops/pfm/internal/engine/opencode"
	"hostops/pfm/internal/gather"
)

type reapTestMatcher struct{ id pfmengine.ID }

func (matcher reapTestMatcher) IsCommand(argv []string, binaries ...string) bool {
	switch matcher.id {
	case pfmengine.Codex:
		return gather.IsCodexCommand(argv, binaries...)
	case pfmengine.Opencode:
		return opencodeengine.Matcher{}.IsCommand(argv, binaries...)
	default:
		return gather.IsClaudeCommand(argv, binaries...)
	}
}

func init() {
	gather.RegisterMatcher(pfmengine.Claude, reapTestMatcher{id: pfmengine.Claude})
	gather.RegisterMatcher(pfmengine.Codex, reapTestMatcher{id: pfmengine.Codex})
	gather.RegisterMatcher(pfmengine.Opencode, reapTestMatcher{id: pfmengine.Opencode})
}

func TestProcessTreeLoadsEveryRegisteredEngineMatcher(t *testing.T) {
	tree, err := NewProcessTree(reapTestProc{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range pfmengine.All() {
		if _, ok := tree.matchers[id]; !ok {
			t.Errorf("process tree omitted matcher for %s", id)
		}
	}
}

type reapTestProc struct{}

func (reapTestProc) PIDs() ([]int, error)                   { return nil, nil }
func (reapTestProc) Cmdline(int) ([]string, error)          { return nil, nil }
func (reapTestProc) Environ(int) (map[string]string, error) { return nil, nil }
func (reapTestProc) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (reapTestProc) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }
