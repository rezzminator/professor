package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// A seat named positionally keeps every flag written after its name: the
// flag parser must not stop at the name and silently birth a default chat.
func TestChatNewReadsFlagsAfterAPositionalName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "seat-x", "--timeout", "-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf(
			"chat new seat-x --timeout -1 = %d, want 2 (usage: the negative timeout after the name was read); stderr=%q",
			code,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat new") {
		t.Fatalf("stderr = %q, want the chat new usage line", stderr.String())
	}
}

func newWorkbenchRunFixture(t *testing.T, jailRoot string) (string, string) {
	t.Helper()
	root := filepath.Join(jailRoot, "work", "acme")
	dir := filepath.Join(root, "docs", "scribe")
	duo := filepath.Join(root, "docs", "duo")
	for path, body := range map[string]string{
		filepath.Join(root, ".professor", "baseline.json"): "{}",
		paths.WorkbenchManifest(dir):                       `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"):      "You are scribe.",
		paths.WorkbenchManifest(duo):                       `{"prompt":"duo.md","engines":["codex","claude"]}`,
		filepath.Join(duo, ".professor", "duo.md"):         "You are duo.",
		filepath.Join(duo, "CLAUDE.md"):                    "Duo.\n",
		filepath.Join(root, ".claude", "agents", "r.md"):   "ROLE R",
		filepath.Join(root, ".codex", "agents", "r.toml"):  `developer_instructions = "ROLE R"`,
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, duo
}

func TestInternalSeatHostDispatch(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "seat-host"}, &stdout, &stderr)
	if want := "pfm internal seat-host: expected one seat id\n"; code != 1 || stderr.String() != want {
		t.Fatalf("seat-host code=%d stdout=%q stderr = %q, want %q", code, stdout.String(), stderr.String(), want)
	}
}

func TestChatNameNativeDeliveryConvergesWithoutTmuxWindow(t *testing.T) {
	jailTest(t)
	var stderr bytes.Buffer
	delivered := false
	code := applyChatName(
		context.Background(),
		headless.Chat{Backend: "herdr", Socket: "/not-a-tmux-socket", Pane: "w6:p3", Engine: pfmengine.Claude},
		"INTEGRATION",
		func(_ context.Context, target headless.Chat, name string) (int, string, error) {
			delivered = target.Backend == "herdr" && name == "INTEGRATION"
			return 0, "", nil
		},
		&stderr,
	)
	if code != 0 || !delivered {
		t.Fatalf("native name code=%d delivered=%t stderr=%q", code, delivered, stderr.String())
	}
}

func TestReservedSeatLabel(t *testing.T) {
	var got strings.Builder
	for _, label := range []string{"seat:child", "SeAt:child"} {
		for _, args := range [][]string{
			{"chat", "new", "--name", label, "--backend", "invalid"},
			{"chat", "name", "missing", label},
		} {
			var out, stderr bytes.Buffer
			fmt.Fprintf(&got, "%d %s", run(args, &out, &stderr), &stderr)
		}
	}
	if want := "2 pfm chat new: label \"seat:child\" is reserved for durable seat handles\n2 pfm chat name: label \"seat:child\" is reserved for durable seat handles\n2 pfm chat new: label \"SeAt:child\" is reserved for durable seat handles\n2 pfm chat name: label \"SeAt:child\" is reserved for durable seat handles\n"; got.String() != want {
		t.Errorf("reserved labels = %q, want %q", got.String(), want)
	}
}

// exitRecordingVerbs keeps every real chat verb but records exit's target
// instead of running the detached exit choreography on the test process.
type exitRecordingVerbs struct {
	mcpserv.ChatVerbs
	exits chan headless.Chat
}

func (verbs exitRecordingVerbs) Exit(_ context.Context, self headless.Chat) (string, error) {
	verbs.exits <- self
	return "recorded", nil
}

type chatNewCallerVerbs struct {
	mcpserv.ChatVerbs // the verbs chat new never calls: a call panics
	row               compose.Row
}

func (verbs chatNewCallerVerbs) Last(context.Context, pfmchat.LastRequest) (pfmchat.LastResult, error) {
	return pfmchat.LastResult{}, errors.New("unexpected chat last")
}

func (verbs chatNewCallerVerbs) Status(context.Context, pfmchat.StatusRequest) (headless.Status, error) {
	return headless.Status{}, errors.New("unexpected chat status")
}

func (verbs chatNewCallerVerbs) List(context.Context, pfmchat.ListRequest) (pfmchat.ListResult, error) {
	return pfmchat.ListResult{Rows: []compose.Row{verbs.row}, Matched: 1}, nil
}

func (verbs chatNewCallerVerbs) Find(context.Context, pfmchat.FindRequest) ([]pfmchat.TranscriptMatch, error) {
	return nil, errors.New("unexpected chat find")
}

func (verbs chatNewCallerVerbs) Read(
	context.Context,
	string,
	int,
) (headless.Chat, []transcript.Entry, bool, error) {
	return headless.Chat{}, nil, false, errors.New("unexpected chat read")
}

func (verbs chatNewCallerVerbs) Exit(context.Context, headless.Chat) (string, error) {
	return "", errors.New("unexpected chat exit")
}
