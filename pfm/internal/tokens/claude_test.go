package tokens

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashCategoryNamesRunsNotMentions(t *testing.T) {
	cases := map[string]int{
		"go test ./...":                     catBashTest,
		"cd /w && npm run typecheck":        catBashTest,
		".claude/scripts/dev.sh verify pfm": catBashTest,
		".claude/scripts/dev.sh status pfm": catBashOther,
		"git status":                        catBashGit,
		"echo x; gh pr view":                catBashGit,
		"grep -n foo bar.go":                catBashRead,
		"cat a | wc -l":                     catBashRead,
		"echo tsc is the compiler":          catBashOther,
	}
	for command, want := range cases {
		if got := bashCategory(command); got != want {
			t.Errorf("bashCategory(%q) = %s, want %s", command, categories[got], categories[want])
		}
	}
	for command, want := range map[string]bool{"sleep 300": true, "ls; sleep 5": true, "tail -f log": true, "make sleepy": false} {
		if got := bashPoll.MatchString(command); got != want {
			t.Errorf("bashPoll(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestRawValuesReadAsJavaScript(t *testing.T) {
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }
	for text, want := range map[string]bool{
		"": false, "null": false, "false": false, `""`: false, "0": false,
		"-0": false, "1": true, "-2.5": true, `"x"`: true, "{}": true, "[]": true, "true": true,
	} {
		if got := truthy(raw(text)); got != want {
			t.Errorf("truthy(%s) = %v, want %v", text, got, want)
		}
	}
	keys := map[string]string{
		"": "undefined", `"s"`: "s", `{"a":1}`: "[object Object]", `[1,null,"b"]`: "1,,b",
		"true": "true", "null": "null", "1e3": "1000", "0.5": "0.5",
	}
	for text, want := range keys {
		if got := keyString(raw(text)); got != want {
			t.Errorf("keyString(%s) = %q, want %q", text, got, want)
		}
	}
	if got := string(coalesce(nil, raw("null"), raw("0"), raw("1"))); got != "0" {
		t.Errorf("coalesce = %s, want 0: ?? passes over null and absent only", got)
	}
	if got := firstOf(raw("0"), raw(`""`), raw(`"b"`)); got != "b" {
		t.Errorf("firstOf = %q, want b: || passes over every falsy value", got)
	}
	if jsString(raw("5")) != "" || !isString(raw(`"5"`)) || isString(raw("5")) {
		t.Errorf("jsString and isString read only a JSON string")
	}
	if got := stringifiedLength(raw(`{ "a" : "😀" }`)); got != 10 {
		t.Errorf("stringifiedLength = %v, want 10: {\"a\":\"😀\"} in UTF-16 units", got)
	}
	results := map[string]float64{
		`"abc"`: 3,
		`[{"type":"text","text":"ab"},{"type":"image","source":{}},{"type":"other","x":1}]`: 2 + 6000 + float64(
			len(`{"type":"other","x":1}`),
		),
		`{"k":1}`: 7,
		`null`:    0,
	}
	for text, want := range results {
		if got := resultChars(raw(text)); got != want {
			t.Errorf("resultChars(%s) = %v, want %v", text, got, want)
		}
	}
}

func TestToolTargetAndInputString(t *testing.T) {
	long := strings.Repeat("x", 120)
	cases := map[string]string{
		`{"file_path":"/w/a.go","command":"ignored"}`: "/w/a.go",
		`{"command":"go   test\n ./..."}`:             "go test ./...",
		`{"subagent_type":"flights-lander"}`:          "flights-lander",
		`{"n":1,"note":"first string member"}`:        "first string member",
		`{"query":"` + long + `"}`:                    strings.Repeat("x", 99) + "…",
		`{}`:                                          "",
	}
	for input, want := range cases {
		if got := toolTarget(json.RawMessage(input)); got != want {
			t.Errorf("toolTarget(%s) = %q, want %q", input, got, want)
		}
	}
	input := json.RawMessage(`{"a":"","b":7,"c":"x"}`)
	if got := inputString(input, "a", "b", "c"); got != "7" {
		t.Errorf("inputString = %q, want 7: the first truthy field", got)
	}
	if got := inputString(json.RawMessage(`"not an object"`), "a"); got != "" {
		t.Errorf("inputString of a non-object = %q", got)
	}
}

func TestCollectCountsMalformedUndatedAndSyntheticLines(t *testing.T) {
	file := filepath.Join(t.TempDir(), "-w-demo", "s1.jsonl")
	writeTranscript(t, file,
		`{not json`,
		assistant("m1", "claude-sonnet-5", "", 10, 10, 0, 0),
		assistant("m2", "<synthetic>", "2026-09-21T05:00:00Z", 10, 10, 0, 0),
		assistant("m3", "claude-sonnet-5", "2026-09-21T05:00:00Z", 0, 0, 0, 0),
		assistant("m4", "claude-sonnet-5", "2026-09-21T05:00:00Z", 10, 10, 0, 0),
		assistant("m4", "claude-sonnet-5", "2026-09-21T05:00:01Z", 10, 12, 0, 0),
		`{"type":"custom-title","customTitle":"demo title"}`,
	)
	a, _, _ := testAuditor(t)
	c, err := a.collect(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.scan.badLines != 1 || a.scan.noTimestamp != 1 || a.scan.syntheticCalls != 2 {
		t.Errorf(
			"bad %d, undated %d, synthetic %d; want 1, 1, 2",
			a.scan.badLines,
			a.scan.noTimestamp,
			a.scan.syntheticCalls,
		)
	}
	if c.order != 1 || c.usage["message:m4"].usage.Output != 12 || c.title != "demo title" || c.cwd != "/w/demo" {
		t.Errorf("calls %d, m4 output %d, title %q, cwd %q; want one call whose last line counts",
			c.order, c.usage["message:m4"].usage.Output, c.title, c.cwd)
	}
}
