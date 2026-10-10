package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestArchivePrunesOnlyTheOwningSessionField(t *testing.T) {
	const archived = "11111111-1111-4111-8111-111111111111"
	const kept = "22222222-2222-4222-8222-222222222222"
	for _, field := range []string{"sessionId", "sid", "id"} {
		t.Run(field, func(t *testing.T) {
			values := archiveJail(t)
			path := filepath.Join(values.Home, ".claude", "history.jsonl")
			if field == "id" {
				path = filepath.Join(values.Roots[pfmengine.Codex][0], "session_index.jsonl")
			}
			keepLine := `{"` + field + `":"` + kept + `","display":"mentions ` + archived + `"}` + "\r\n"
			unowned := []string{
				`{"` + field + `":"` + archived + `"`,
				`{"display":"mentions ` + archived + `"}`,
				`{"` + field + `":null,"display":"mentions ` + archived + `"}`,
				`{"` + field + `":42,"display":"mentions ` + archived + `"}`,
			}
			if field != "id" {
				unowned = append(unowned, `{"sessionId":"`+archived+`","sid":"`+kept+`"}`)
			}
			retained := keepLine + strings.Join(unowned, "\r\n")
			content := `{"` + field + `":"` + archived + `","display":"owned"}` + "\r\n" + retained
			writeFile(t, path, content)
			runner, err := New(Dependencies{Paths: values, Kills: &fakeKills{}, Proc: emptyProc{}})
			if err != nil {
				t.Fatal(err)
			}
			dropped, unclassified, err := runner.pruneLines(path, []string{archived})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(unclassified) != len(unowned) {
				t.Errorf("unclassified ownership not reported: got=%v want %d lines", unclassified, len(unowned))
			}
			for _, warning := range unclassified {
				if !strings.Contains(warning, "unclassified") {
					t.Errorf("ownership warning lacks context: %q", warning)
				}
			}
			if dropped != 1 || strings.TrimLeft(string(got), " \r\n") != retained {
				t.Fatalf("pruned non-owning UUID or unclassified line: dropped=%d got=%q", dropped, got)
			}
		})
	}
}
