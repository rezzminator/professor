package call

import "fmt"

// Refusal is a chat function's refusal as both of its doors answer it: `pfm chat {verb}` prints Message on its
// verb's stderr line and exits Code; the chat MCP tool returns a receipt of Status, Code and Message. Each
// refusal is worded here once, so the two doors can never word one refusal two ways.
type Refusal struct {
	Status  string
	Code    int
	Message string
}

func (refusal Refusal) Error() string { return refusal.Message }

// The status words a refusal's receipt carries.
const (
	StatusNotFound    = "not_found"
	StatusDead        = "dead"
	StatusNotAnswered = "not_answered"
	StatusAmbiguous   = "ambiguous"
	StatusError       = "error"
)

// CodeLookupFailed is the code of a target lookup that could not look (a fleet that could not be read): the exit
// `pfm chat` has always given it, the number CodeAmbiguous and a usage error share.
const CodeLookupFailed = 2

// UnknownChat refuses a name no chat answers to.
func UnknownChat(name string) Refusal {
	return Refusal{Status: StatusNotFound, Code: CodeUnknown, Message: fmt.Sprintf("no chat named %q", name)}
}

// AmbiguousChat refuses a name several chats answer to; candidates lists them.
func AmbiguousChat(candidates string) Refusal {
	return Refusal{Status: StatusAmbiguous, Code: CodeAmbiguous, Message: candidates}
}

// LookupFailed is a target lookup that could not look, as both doors answer it: never a refusal of the name, so
// the MCP answers it as a tool error carrying this receipt, the CLI exits its code.
func LookupFailed(err error) Refusal {
	return Refusal{Status: StatusError, Code: CodeLookupFailed, Message: err.Error()}
}

// NotRunning refuses to drive or read the pane of a chat that has none running.
func NotRunning(name string) Refusal {
	return Refusal{Status: StatusDead, Code: CodeDead, Message: fmt.Sprintf("%q is not running", name)}
}

// DeadChat refuses a call into a dead chat whose transcript holds no turn to resume, naming how to revive it.
func DeadChat(name, engine, session string) Refusal {
	return Refusal{Status: StatusDead, Code: CodeDead, Message: fmt.Sprintf(
		"%q is dead (engine %s, session %s); revive it with: pfm chat open %s", name, engine, session, name,
	)}
}

// DeadResumable refuses a live delivery into a dead chat whose transcript takes a resumed turn: only `pfm chat call`
// from a shell appends it.
func DeadResumable(name string) Refusal {
	return Refusal{Status: StatusDead, Code: CodeDead, Message: fmt.Sprintf(
		"%q is dead; `pfm chat call %s` from a shell appends the message to its transcript, "+
			"or revive it with: pfm chat open %s", name, name, name,
	)}
}

// NotAnswered refuses a last answer of a chat whose transcript holds none yet.
func NotAnswered(name string) Refusal {
	return Refusal{Status: StatusNotAnswered, Code: CodeDead, Message: fmt.Sprintf("%q has not answered yet", name)}
}

// NoTranscript refuses a last answer of a chat that has not written a transcript yet.
func NoTranscript(name string) Refusal {
	return Refusal{
		Status: StatusNotAnswered, Code: CodeDead, Message: fmt.Sprintf("%q has not written a transcript yet", name),
	}
}

// ExitRefusal is the refusal an exit code of `pfm chat {verb}` stands for, with the verb's own message: CodeUnknown
// and CodeDead are refusals, any other code is not (ok false).
func ExitRefusal(code int, message string) (Refusal, bool) {
	switch code {
	case CodeUnknown:
		return Refusal{Status: StatusNotFound, Code: code, Message: message}, true
	case CodeDead:
		return Refusal{Status: StatusDead, Code: code, Message: message}, true
	}
	return Refusal{}, false
}
