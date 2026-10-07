package installer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
)

// beyondFloat64 is 2^53+1, the first integer a float64 cannot hold: decoded
// into a Go `any` without UseNumber it comes back as 9007199254740992.
const beyondFloat64 = "9007199254740993"

func requireKeepsBeyondFloat64(t *testing.T, door string, rewritten []byte) {
	t.Helper()
	if !strings.Contains(string(rewritten), beyondFloat64) {
		t.Fatalf("%s rewrote a user-owned number %s as something else: %s", door, beyondFloat64, rewritten)
	}
}

func TestUnmarshalKeepingNumbersIsAsStrictAsUnmarshal(t *testing.T) {
	t.Parallel()
	var document map[string]any
	if err := unmarshalKeepingNumbers([]byte(`{"counter":`+beyondFloat64+`}`), &document); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(document["counter"]) != beyondFloat64 {
		t.Fatalf("counter decoded as %v, want %s", document["counter"], beyondFloat64)
	}
	for _, raw := range []string{``, `{`, `{} {}`, `{} trailing`, `{"a":1}]`} {
		if err := unmarshalKeepingNumbers([]byte(raw), &document); err == nil {
			t.Errorf("unmarshalKeepingNumbers(%q) accepted input json.Unmarshal refuses", raw)
		}
	}
	if err := unmarshalKeepingNumbers([]byte("{}\n  \n"), &document); err != nil {
		t.Errorf("trailing whitespace refused: %v", err)
	}
}

func TestCodexHooksRewriteKeepsIntegersBeyondFloat64(t *testing.T) {
	t.Parallel()
	// The Codex hook file is rewritten only to take something away now, so
	// the fixture carries the retired appendix hook for the pass to remove.
	home := t.TempDir()
	raw := []byte(`{"counter":` + beyondFloat64 + `,"hooks":{"SessionStart":[{"matcher":` +
		strconv.Quote(codexappendix.Matcher) + `,"hooks":[{"type":"command","command":` +
		strconv.Quote(codexappendix.Command(home)) + `}]}]}}`)
	updated, changed, _, err := updateCodexHooks(raw, home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateCodexHooks changed=%v err=%v; want a rewrite", changed, err)
	}
	requireKeepsBeyondFloat64(t, "updateCodexHooks", updated)
}

func TestJSONNumberIsMatchesEitherDecodedForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{json.Number("10"), true},
		{json.Number("10.0"), true},
		{float64(10), true},
		{json.Number("11"), false},
		{float64(9), false},
		{"10", false},
		{nil, false},
	} {
		if got := jsonNumberIs(tc.value, 10); got != tc.want {
			t.Errorf("jsonNumberIs(%#v, 10) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
