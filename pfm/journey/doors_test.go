//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// A chat reaches another through two doors: `pfm chat {verb}` run in its shell tool (the CLI door) and
// a tool of pfm's chat MCP server (the MCP door). The steps of herdr-mesh play each chat function
// through both from inside a live Claude chat in Herdr, as a real Claude makes the calls, and read
// what each door answered from the caller's own transcript.

// cli is a fake-engine shell-tool call that runs command: the CLI door of a chat function.
func cli(command string) map[string]any {
	return map[string]any{
		"type": "tool_call", "tool": "Bash", "perform": true,
		"input": map[string]any{"command": command},
	}
}

// answer is what one door answered a call: the CLI's exit code and its stdout and stderr together, or
// the MCP tool's text and whether it is a tool error (Code 1, else 0).
type answer struct {
	Code int
	Text string
}

// play types steps into term's chat, s's own, as one turn, waits for the turn's reply, keeps it as the
// mark of the chat's tab, and returns what each of its tool calls answered, in order.
func play(c *harness.Ctx, term *harness.Terminal, s *State, steps ...map[string]any) []answer {
	return played(c, s, steps, func(turn, reply string) {
		term.Type(turn + "\r")
		term.WaitFor("⏺ "+reply, 90*time.Second)
	})
}

// played delivers steps as one turn of s's chat that ends in a reply, through deliver, which returns
// once the reply is in; it keeps the reply as the mark of the chat's tab and returns what each of the
// turn's tool calls answered, in order.
func played(c *harness.Ctx, s *State, steps []map[string]any, deliver func(turn, reply string)) []answer {
	name, session := s.Name, s.Chat.Session
	calls := 0
	for _, step := range steps {
		if step["type"] == "tool_call" || step["type"] == "mcp" {
			calls++
		}
	}
	before := len(toolResults(c, session))
	reply := c.Unique("played")
	deliver(directive(append(steps, turnStep(reply))...), reply)
	s.Mesh.Mark = reply
	var got []toolResult
	c.Poll(fmt.Sprintf("%d tool results in %s's transcript", before+calls, name), stepBound, func() (bool, string) {
		got = toolResults(c, session)
		return len(got) == before+calls, fmt.Sprintf("%d results", len(got))
	})
	logSlowest(c, name, steps, got[before:])
	var answers []answer
	for index, step := range steps {
		if step["type"] != "tool_call" && step["type"] != "mcp" {
			continue
		}
		result := got[before+len(answers)]
		if step["type"] == "mcp" {
			answers = append(answers, mcpAnswer(c, name, result))
			continue
		}
		text, code := result.Content, 0
		head, rest, found := strings.Cut(text, "\n")
		if result.IsError && found && strings.HasPrefix(head, "Exit code ") {
			n, err := strconv.Atoi(strings.TrimPrefix(head, "Exit code "))
			if err != nil {
				c.Failf("%s's shell result %d of the turn heads %q, not an exit code", name, index, head)
			}
			text, code = rest, n
		}
		answers = append(answers, answer{Code: code, Text: text})
	}
	return answers
}

// mcpAnswer decodes an MCP tool result as the fake Claude records it: the CallToolResult JSON.
func mcpAnswer(c *harness.Ctx, name string, result toolResult) answer {
	var decoded struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil || len(decoded.Content) != 1 {
		c.Failf("%s's MCP tool result is no one-text CallToolResult (%v): %q", name, err, result.Content)
	}
	code := 0
	if decoded.IsError {
		code = 1
	}
	return answer{Code: code, Text: decoded.Content[0].Text}
}

// toolResult is one tool_result block of a Claude transcript.
type toolResult struct {
	Content string    `json:"content"`
	IsError bool      `json:"is_error"`
	At      time.Time `json:"-"` // its record's timestamp
}

// logSlowest logs the turn's five slowest tool calls, each timed from the result before it: where a
// step's time goes.
func logSlowest(c *harness.Ctx, name string, steps []map[string]any, results []toolResult) {
	type timed struct {
		took time.Duration
		step string
	}
	var calls []timed
	for index, step := range steps {
		if step["type"] != "tool_call" && step["type"] != "mcp" || len(calls) >= len(results) {
			continue
		}
		at := len(calls)
		var took time.Duration
		if at > 0 {
			took = results[at].At.Sub(results[at-1].At)
		}
		calls = append(calls, timed{took, fmt.Sprint(steps[index]["tool"], " ", steps[index]["input"])})
	}
	slices.SortFunc(calls, func(a, b timed) int { return int(b.took - a.took) })
	var slowest []string
	for _, call := range calls[:min(5, len(calls))] {
		slowest = append(slowest, fmt.Sprintf("%s %.80s", call.took.Round(time.Millisecond), call.step))
	}
	c.Logf("%s's slowest calls of the turn: %s", name, strings.Join(slowest, " | "))
}

// toolResults is every tool result in the Claude transcript session's file, in order.
func toolResults(c *harness.Ctx, session string) []toolResult {
	var results []toolResult
	for _, line := range strings.Split(read(c, "sh", "-c", "cat ~/.claude/projects/*/"+session+".jsonl"), "\n") {
		var record struct {
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		var blocks []struct {
			Type string `json:"type"`
			toolResult
		}
		if json.Unmarshal([]byte(line), &record) != nil || json.Unmarshal(record.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_result" {
				block.At = record.Timestamp
				results = append(results, block.toolResult)
			}
		}
	}
	return results
}

// receipt is a chat function's MCP answer decoded as pfm's action receipt: status, code and message.
type receipt struct {
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpReceipt is the receipt of the latest MCP tool call of the chat name, read from its Claude
// transcript, session's file.
func mcpReceipt(c *harness.Ctx, name, session string) receipt {
	results := toolResults(c, session)
	var decoded receipt
	if len(results) == 0 {
		c.Failf("no tool result in %s's transcript %s", name, session)
	}
	got := mcpAnswer(c, name, results[len(results)-1])
	if err := json.Unmarshal([]byte(got.Text), &decoded); err != nil {
		c.Failf("%s's last MCP answer is no receipt (%v): %q", name, err, got.Text)
	}
	return decoded
}

// door is one cell of the chat-function matrix: one function played through one door from inside a
// live Claude chat, and the answer that door must give.
type door struct {
	step  map[string]any
	want  answer              // the exact answer, when check is nil
	check func(answer) string // else: "" when the answer is right, or what is wrong with it
}

// playDoors plays every cell as one turn of s's chat in term, fails naming each cell that answered
// other than it must, and returns the answers.
func playDoors(c *harness.Ctx, term *harness.Terminal, s *State, label string, cells ...door) []answer {
	steps := make([]map[string]any, len(cells))
	for index, cell := range cells {
		steps[index] = cell.step
	}
	answers := play(c, term, s, steps...)
	var wrong []string
	for index, cell := range cells {
		got, problem := answers[index], ""
		switch {
		case cell.check != nil:
			problem = cell.check(got)
		case got != cell.want:
			problem = fmt.Sprintf("want code %d, text %q", cell.want.Code, cell.want.Text)
		}
		if problem != "" {
			encoded, err := json.Marshal(cell.step)
			if err != nil {
				c.Failf("encode the %s cell %d: %v", label, index, err)
			}
			wrong = append(wrong, fmt.Sprintf("%s\n  got code %d, text %q\n  %s", encoded, got.Code, got.Text, problem))
		}
	}
	c.Logf("%s: %d door cells played, %d wrong", label, len(cells), len(wrong))
	if len(wrong) > 0 {
		c.Failf("%s: %d of %d door cells answered wrong, read from %s's transcript:\n%s", label, len(wrong),
			len(cells), s.Name, strings.Join(wrong, "\n"))
	}
	return answers
}

// cliIs is the CLI door running command, answering exactly code and text.
func cliIs(command string, code int, text string) door {
	return door{step: cli(command), want: answer{Code: code, Text: text}}
}

// cliMatch is the CLI door running command, answering code and a text pattern matches whole.
func cliMatch(command string, code int, pattern string) door {
	return door{step: cli(command), check: matches(code, pattern)}
}

// mcpJSON is the MCP door's tool called with input, answering with no tool error a JSON value equal
// to want.
func mcpJSON(tool string, input map[string]any, want any) door {
	return door{step: mcpStep(tool, input), check: jsonIs(0, "", want)}
}

// matches is a check that the answer's code is code and pattern matches its whole text.
func matches(code int, pattern string) func(answer) string {
	compiled := regexp.MustCompile("^(?:" + pattern + ")$")
	return func(got answer) string {
		if got.Code != code || !compiled.MatchString(got.Text) {
			return fmt.Sprintf("want code %d, text matching %s", code, compiled)
		}
		return ""
	}
}

// jsonIs is a check that the answer's code is code and its text is a JSON value equal to want followed
// by tail; a want map holding idleSeconds for a field asks only that the field is a number.
func jsonIs(code int, tail string, want any) func(answer) string {
	return func(got answer) string {
		body, found := strings.CutSuffix(got.Text, tail)
		var decoded, wanted any
		encoded, err := json.Marshal(want)
		if err != nil {
			return fmt.Sprintf("encode the wanted value: %v", err)
		}
		if err := json.Unmarshal(encoded, &wanted); err != nil {
			return fmt.Sprintf("decode the wanted value: %v", err)
		}
		if err := json.Unmarshal([]byte(body), &decoded); err != nil || !found || got.Code != code {
			return fmt.Sprintf("want code %d, a JSON value then %q (%v): %s", code, tail, err, encoded)
		}
		wantedFields, isMap := wanted.(map[string]any)
		if fields, ok := decoded.(map[string]any); ok && isMap {
			for key, value := range wantedFields {
				if _, number := fields[key].(float64); number && value == idleSeconds {
					fields[key] = idleSeconds
				}
			}
		}
		if !reflect.DeepEqual(decoded, wanted) {
			return fmt.Sprintf("want the JSON value %s", encoded)
		}
		return ""
	}
}

// idleSeconds stands in a wanted value for a number a clock or the rest of the fleet decides: the
// idle_seconds of a status, the counts of chats elsewhere and killed in a listing.
const idleSeconds = "a number"

// notFound is the CLI door's answer for a name no chat holds.
func notFound(name string) string {
	return name + "\tnot-found\npfm chat: no chat named \"" + name + "\"\n"
}

// statusJSON is a chat's status as both doors print it, less the idle seconds a clock decides.
type statusJSON = map[string]any

// cliStatus is the CLI door's `pfm chat status {target} --json`, answering code and want.
func cliStatus(target string, code int, want statusJSON) door {
	return door{step: cli("pfm chat status " + target + " --json"), check: statusIs(code, want)}
}

// mcpStatus is the MCP door's status of target, answering want; a chat not alive (the CLI's exit 3)
// carries that code in the receipt.
func mcpStatus(target string, code int, want statusJSON) door {
	if code != 0 {
		coded := statusJSON{"code": code}
		for key, value := range want {
			coded[key] = value
		}
		want = coded
	}
	return door{step: mcpStep("status", map[string]any{"target": target}), check: statusIs(0, want)}
}

// mcpNotAnswered is the MCP door's last of target, the chat named name that has not answered yet.
func mcpNotAnswered(target, name string) door {
	return mcpJSON("last", map[string]any{"target": target}, map[string]any{
		"code": 3, "message": fmt.Sprintf("%q has not answered yet", name), "status": "not_answered", "target": target,
	})
}

// herdrUnknown is the state source a live Herdr chat's status names while Herdr knows no state for
// its agent, so the transcript decided it.
const herdrUnknown = "transcript (Herdr unknown)"

// statusIs is a check that a status answered code and want. A live chat's (want names its socket)
// state may come from Herdr or, while Herdr knows none for its agent, from its transcript, named in
// state_source: which one answers is Herdr's timing, so either passes.
func statusIs(code int, want statusJSON) func(answer) string {
	if _, live := want["socket"]; !live {
		return jsonIs(code, "", want)
	}
	unknown := statusJSON{"state_source": herdrUnknown}
	for key, value := range want {
		unknown[key] = value
	}
	return oneOf(jsonIs(code, "", want), jsonIs(code, "", unknown))
}

// withLast is want with its last field set to last.
func withLast(want statusJSON, last string) statusJSON {
	copied := statusJSON{"last": last}
	for key, value := range want {
		if key != "last" {
			copied[key] = value
		}
	}
	return copied
}

// readTail is a check that `pfm chat read --json` answered name's transcript (engine's, at path)
// holding want, every entry less its time, and truncated or not.
func readTail(name, engine, path string, truncated bool, want ...map[string]any) func(answer) string {
	return func(got answer) string {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got.Text), &decoded); err != nil || got.Code != 0 {
			return fmt.Sprintf("want code 0 and a JSON read (%v)", err)
		}
		entries, _ := decoded["entries"].([]any)
		for _, entry := range entries {
			if fields, ok := entry.(map[string]any); ok {
				delete(fields, "timestamp")
			}
		}
		decoded["entries"] = entries
		return jsonIs(0, "", map[string]any{
			"engine": engine, "entries": append([]map[string]any{}, want...), "name": name, "path": path,
			"truncated": truncated,
		})(answer{Text: mustJSON(decoded)})
	}
}

// mustJSON is value as JSON; a value that cannot be encoded reads as its error.
func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err.Error()
	}
	return string(encoded)
}

// rule is the 40-column line Claude's composer is drawn between.
var rule = strings.Repeat("─", 40)

// captureHead is a check that the MCP door's capture of pane, its last tail lines, is a Herdr snapshot
// of exactly tail lines, truncated, whose first lines are head: the last tail lines of the CLI door's
// capture of the same pane.
func captureHead(pane, sock string, tail int, head ...string) func(answer) string {
	return func(got answer) string {
		var decoded struct {
			Bytes      int    `json:"bytes"`
			Code       int    `json:"code"`
			Message    string `json:"message"`
			Pane       string `json:"pane"`
			SocketPath string `json:"socket_path"`
			Status     string `json:"status"`
			Text       string `json:"text"`
			Truncated  bool   `json:"truncated"`
		}
		err := json.Unmarshal([]byte(got.Text), &decoded)
		lines := strings.Split(decoded.Text, "\n")
		if err != nil || got.Code != 0 || decoded.Status != "ok" || decoded.Code != 0 || decoded.Message != "" ||
			!decoded.Truncated || decoded.Pane != pane || decoded.SocketPath != sock ||
			decoded.Bytes != len(decoded.Text) || len(lines) != tail+1 || lines[tail] != "" ||
			!reflect.DeepEqual(lines[:len(head)], head) {
			return fmt.Sprintf("want status ok, code 0, no message, truncated, pane %s on %s, bytes its text's "+
				"length, text %d lines then a newline, opening %q (%v)", pane, sock, tail, head, err)
		}
		return ""
	}
}
