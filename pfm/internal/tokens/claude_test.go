package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestInputString(t *testing.T) {
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
		assistant("m5", "claude-sonnet-5", "", 10, 10, 0, 0),
		`{"type":"custom-title","customTitle":"demo title"}`,
		// a clock field that is no number: the total stands, the line counts as bad
		`{"type":"cost-state","totalCostUSD":1.5,"startTime":"2026-09-21T05:00:00Z","totalDuration":1000}`,
	)
	fileTime := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, fileTime, fileTime); err != nil {
		t.Fatal(err)
	}
	a, _, _ := testAuditor(t)
	c, err := a.collect(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for key := range c.usage {
		kept = append(kept, key)
	}
	for _, key := range []string{"message:m1", "message:m4", "message:m5"} {
		if c.usage[key] == nil {
			t.Fatalf("collect kept calls %v, want message:m1, message:m4 and message:m5; %s is missing", kept, key)
		}
	}
	// An undated call is kept: placed at the previous record's time, else the file's.
	if got, want := c.usage["message:m1"].ts, float64(fileTime.UnixMilli()); got != want {
		t.Errorf("m1 (undated, first record) ts %v, want the file time %v", got, want)
	}
	m4Time := float64(time.Date(2026, 9, 21, 5, 0, 1, 0, time.UTC).UnixMilli())
	if got, want := c.usage["message:m5"].ts, m4Time; got != want {
		t.Errorf("m5 (undated after m4) ts %v, want m4's %v", got, want)
	}
	if a.scan.badLines != 2 || a.scan.noTimestamp != 2 || a.scan.syntheticCalls != 2 {
		t.Errorf(
			"bad %d, undated %d, synthetic %d; want 2, 2, 2",
			a.scan.badLines,
			a.scan.noTimestamp,
			a.scan.syntheticCalls,
		)
	}
	if c.harnessUSD == nil || *c.harnessUSD != 1.5 || len(c.anchors.States()) != 1 {
		t.Errorf("harnessUSD %v, cost-states %v; want 1.5 and one state", c.harnessUSD, c.anchors.States())
	}
	if c.order != 3 || c.usage["message:m4"].usage.Output != 12 || c.title != "demo title" || c.cwd != "/w/demo" {
		t.Errorf("calls %d, m4 output %d, title %q, cwd %q; want three calls, m4's last line counting",
			c.order, c.usage["message:m4"].usage.Output, c.title, c.cwd)
	}
}

// TestCollectCountsOneCallPerRequestAcrossACompaction: the fixture is one
// agent transcript across a compaction — msg_A split over two lines, msg_B, a
// <synthetic> line, an unbilled line, the compact_boundary and its summary,
// msg_C, req_D carrying only a request id, a malformed line and an undated
// tail. collect keeps four calls: A, B, C, D.
func TestCollectCountsOneCallPerRequestAcrossACompaction(t *testing.T) {
	a, _, _ := testAuditor(t)
	c, err := a.collect(fixture(t, "compacted-agent.jsonl"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.order != 4 || len(c.usage) != 4 {
		t.Fatalf("collect counts %d calls (%d keys); want 4 (msg_A, msg_B, msg_C, req_D)", c.order, len(c.usage))
	}
}

func TestMalformedClaudeUsageIsReportedAsGap(t *testing.T) {
	file := writeTranscript(
		t,
		filepath.Join(t.TempDir(), "s1.jsonl"),
		assistant("good", "claude-sonnet-5", "2026-09-21T05:00:00Z", 10, 20, 0, 0),
		`{"type":"assistant","timestamp":"2026-09-21T05:01:00Z","message":{"id":"bad","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"tu_bad","name":"Read","input":{"file_path":"/a"}}],"usage":{"input_tokens":"broken","output_tokens":20}}}`,
		`{"type":"user","timestamp":"2026-09-21T05:01:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_bad","content":"abc"}]}}`,
		`{"type":"assistant"}`,
		`{"type":"assistant","message":{"usage":null}}`,
		`{"type":"assistant","message":{}}`,
	)
	a, _, _ := testAuditor(t)
	c, err := a.collect(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.scan.badLines != 1 || !strings.Contains(a.gapsLine(), "1 malformed lines") {
		t.Errorf("badLines %d, gaps %q; want one malformed usage gap", a.scan.badLines, a.gapsLine())
	}
	if len(c.usage) != 1 || c.usage["message:good"] == nil {
		t.Errorf("usage = %v; want only the valid call", c.usage)
	}
	// The malformed usage costs only the call's usage: the tool use it carried is still registered,
	// so the result that answers it names its tool.
	var tools []string
	for _, e := range c.seq {
		if e.tool != "" {
			tools = append(tools, e.tool)
		}
	}
	if len(tools) != 1 || tools[0] != "Read" {
		t.Errorf("tool results = %v; want one, naming Read through the malformed call's tool_use", tools)
	}
}

// TestClaudeContentDecodesEachBlockOnItsOwn: one odd element of a content list
// never hides its siblings (INTENDED: the list used to fail whole and count
// as its JSON, where the Node engine read each block by its type).
func TestClaudeContentDecodesEachBlockOnItsOwn(t *testing.T) {
	total, ok := blockChars(json.RawMessage(`[{"type":"text","text":"ab"},7]`))
	if !ok || total != 3 {
		t.Fatalf("blockChars = %v, %t; want 3 (the text's 2 and the number's JSON 1), true", total, ok)
	}
}

// TestClaudeTitleIsTheSharedTitleRule: tokens names a transcript by
// transcript.TitleOf, the last title record of either kind (INTENDED: an
// agent-name used to lose to any earlier custom-title).
func TestClaudeTitleIsTheSharedTitleRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titled.jsonl")
	content := `{"type":"custom-title","customTitle":"Desk"}` + "\n" + `{"type":"agent-name","agentName":"Agent"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &auditor{scan: newScanState(), loc: testZone}
	c, err := a.collect(path, map[string]string{})
	if err != nil || c.title != "Agent" {
		t.Fatalf("collect title = %+v, %v; want Agent", c, err)
	}
}
