package gather

import (
	"fmt"
	"sort"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Matcher recognizes one engine's process command.
type Matcher interface {
	IsCommand(argv []string, binaries ...string) bool
}

var matchers = map[pfmengine.ID]Matcher{}

func RegisterMatcher(id pfmengine.ID, matcher Matcher) {
	if _, duplicate := matchers[id]; duplicate {
		panic(fmt.Sprintf("gather: process matcher for engine %q registered twice", id))
	}
	matchers[id] = matcher
}

func MatcherFor(id pfmengine.ID) (Matcher, error) {
	matcher, ok := matchers[id]
	if !ok {
		return nil, fmt.Errorf("engine %s: no process matcher registered", id)
	}
	return matcher, nil
}

func RegisteredMatchers() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(matchers))
	for id := range matchers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}
