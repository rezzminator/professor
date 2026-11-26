package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// testWarningHint is the tail warningFilter.warn ends an unignored row with,
// for a filter whose config path is pfm.config.json.
func testWarningHint(id warningID) string {
	return ` · don't want this? add "` + string(id) + `" to doctor.ignoreWarnings in pfm.config.json`
}

func TestPrintLeakTermsDoctor(t *testing.T) {
	const fix = "fix: write {terms} by hand, one lowercase regex per line ('#' comments), arming a term only once " +
		"git -C {clone} grep -iE '{term}' finds nothing"
	hint := testWarningHint(warnLeakTerms)
	for _, test := range []struct {
		name     string
		gate     bool
		terms    *string
		ignored  bool
		want     string
		warnings int
	}{
		{"no leak gate", false, nil, false, "doctor: leak_terms does not apply: {clone} has no scripts/leak-check.sh\n", 0},
		{
			"absent", true, nil,
			false,
			"doctor: leak_terms ABSENT {terms} — the pre-push leak gate refuses every push — " + fix + hint + "\n", 1,
		},
		{
			"absent and ignored", true, nil, true,
			"doctor: leak_terms ABSENT {terms} — the pre-push leak gate refuses every push IGNORED " +
				"(doctor.ignoreWarnings)\n", 0,
		},
		{
			"not a regular file", true, nil, false,
			"doctor: leak_terms BROKEN {terms} is not a regular file — the pre-push leak gate refuses every push" +
				hint + "\n", 1,
		},
		{
			"zero usable terms", true, ptr("# a comment\n\n   \n!ignore-token foo\n"), false,
			"doctor: leak_terms EMPTY {terms} has zero usable terms — the pre-push leak gate refuses every push — " +
				fix + hint + "\n", 1,
		},
		{
			"terms present", true, ptr("# names\nacme\r\nwidget-co\n!ignore-path docs/x.md\n!identity t@example\\.com\n"), false,
			"doctor: leak_terms ok {terms} terms=2 identities=1\n", 0,
		},
		{
			"structural only mixed with a term", true, ptr("!structural-only\nacme\n"), false,
			"doctor: leak_terms BROKEN {terms}: !structural-only cannot be mixed with private terms or ignores — " +
				"the pre-push leak gate refuses every push" + hint + "\n", 1,
		},
		{
			"unknown directive", true, ptr("acme\n!ignore-tokens foo\n"), false,
			"doctor: leak_terms BROKEN {terms}: unknown directive !ignore-tokens foo — " +
				"the pre-push leak gate refuses every push" + hint + "\n", 1,
		},
		{
			"term without a trailing newline", true, ptr("!identity t@example\\.com\nacme"), false,
			"doctor: leak_terms ok {terms} terms=1 identities=1\n", 0,
		},
		{
			"no identity", true, ptr("acme\n"), false,
			"doctor: leak_terms NO-IDENTITY {terms} names no address commits may be pushed under — " +
				"the pre-push leak gate refuses every push — fix: add one line '!identity {address regex}' to {terms} " +
				"per address this host commits under, matched whole against each new commit's author and committer" +
				hint + "\n", 1,
		},
		{
			"structural only mixed with an identity", true, ptr("!structural-only\n!identity t@example\\.com\n"), false,
			"doctor: leak_terms BROKEN {terms}: !structural-only cannot be mixed with private terms or ignores — " +
				"the pre-push leak gate refuses every push" + hint + "\n", 1,
		},
		{
			"structural only", true, ptr("!structural-only\n"), false,
			"doctor: leak_terms structural-only {terms} — private terms deliberately off\n", 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := t.TempDir()
			if test.gate {
				writeCloneFile(t, clone, "scripts/leak-check.sh", "#!/bin/bash\n")
			}
			if test.terms != nil {
				writeCloneFile(t, clone, "scripts/leak-terms.txt", *test.terms)
			}
			if test.name == "not a regular file" {
				if err := os.MkdirAll(filepath.Join(clone, "scripts", "leak-terms.txt"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			filter := warningFilter{
				ignored:    map[warningID]bool{warnLeakTerms: test.ignored},
				configPath: config.FileName,
			}
			var out bytes.Buffer
			warnings := printLeakTermsDoctor(&out, clone, filter)
			want := bytes.ReplaceAll([]byte(test.want), []byte("{terms}"),
				[]byte(filepath.Join(clone, "scripts", "leak-terms.txt")))
			want = bytes.ReplaceAll(want, []byte("{clone}"), []byte(clone))
			if out.String() != string(want) || warnings != test.warnings {
				t.Fatalf("got %q warnings=%d\nwant %q warnings=%d", out.String(), warnings, want, test.warnings)
			}
		})
	}
}
