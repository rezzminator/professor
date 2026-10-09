package transcript

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// codexOutputHead is how much of a tool output's text the failure rule reads.
const codexOutputHead = 400

var codexExitCode = regexp.MustCompile(
	`(?:Process exited with code|Exit code:?)[\s\x{85}\x{a0}\x{1c}-\x{1f}\p{Z}]*(-?[0-9]+)`,
)

// CodexToolOutput is a Codex tool call's output read the one way every reader
// reads it. text is the output as a string, a JSON-object string by its
// "output" member, any other form by FlatContent. failed is the one failed-call
// rule: a nonzero exit code (the object's metadata.exit_code, else "Process
// exited with code N" or "Exit code: N" within the text's first 400
// characters), text that starts "Script failed" or "Script error", or an
// apply_patch verification failure within those characters.
func CodexToolOutput(output json.RawMessage) (text string, failed bool) {
	text = FlatContent(output)
	var exitCode json.RawMessage
	if wrapped, ok := pyvalue.String(output); ok && strings.HasPrefix(wrapped, "{") {
		if members, isObject := jsvalue.InsertionMembers(json.RawMessage(wrapped)); isObject {
			for _, member := range members {
				switch member.Key {
				case "output":
					text = pyvalue.Str(member.Value)
				case "metadata":
					if metadata, ok := jsvalue.InsertionMembers(member.Value); ok {
						for _, field := range metadata {
							if field.Key == "exit_code" {
								exitCode = field.Value
							}
						}
					}
				}
			}
		}
	}
	head := text
	if runes := []rune(text); len(runes) > codexOutputHead {
		head = string(runes[:codexOutputHead])
	}
	if pyvalue.IsNull(exitCode) {
		if match := codexExitCode.FindStringSubmatch(head); match != nil {
			code, _ := strconv.Atoi(match[1])
			exitCode = json.RawMessage(strconv.Itoa(code))
		}
	}
	return text, NonzeroExit(exitCode) || strings.HasPrefix(head, "Script failed") ||
		strings.HasPrefix(head, "Script error") ||
		strings.Contains(head, "apply_patch verification failed")
}

// NonzeroExit reports an exit code that names a failure: any value but an
// absent one, null, false or a zero.
func NonzeroExit(code json.RawMessage) bool {
	code = bytes.TrimSpace(code)
	return !pyvalue.IsNull(code) && (pyvalue.Bool(code) || code[0] == '"' || code[0] == '[' || code[0] == '{')
}
