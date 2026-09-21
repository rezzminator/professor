package chat

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeOpenTmux is action.TmuxClient for the open doors' jailed tests: it
// records every chat server the detached door asks for and never touches a
// real socket.
type fakeOpenTmux struct {
	alive   map[string]bool
	created []action.ChatServer
}

func (fake *fakeOpenTmux) ListPanes(context.Context, string) ([]action.ActionPane, error) {
	return nil, nil
}

func (fake *fakeOpenTmux) SocketAlive(_ context.Context, socket string) bool {
	return fake.alive[socket]
}

func (fake *fakeOpenTmux) KillPane(context.Context, string, string) error { return nil }

func (fake *fakeOpenTmux) KillServer(context.Context, string) error { return nil }

func (fake *fakeOpenTmux) SetWindowSizeLatest(context.Context, string) error { return nil }

func (fake *fakeOpenTmux) SelectWindow(context.Context, string, int) error { return nil }

func (fake *fakeOpenTmux) CreateChatServer(_ context.Context, server action.ChatServer) error {
	fake.created = append(fake.created, server)
	return nil
}

// emptyProcesses is an action.ProcessTable that reports no processes: a jailed
// open must never sweep the operator's real ones.
type emptyProcesses struct{}

func (emptyProcesses) Processes(context.Context) ([]action.Process, error) { return nil, nil }

func (emptyProcesses) Terminate(int) error { return nil }

// openStub is what stubOpenExecutor observed: the Heal the preparation wired
// (nil means a wedged Codex thread would resume amnesiac) and every thread id
// it was actually called with.
type openStub struct {
	heal   action.HealFunc
	healed []string
}

// stubOpenExecutor replaces the package's executor constructor for the length
// of t. Everything the preparation built is kept — the Codex heal above all,
// wrapped so the test can count its calls instead of reading a real Codex
// store — while tmux and the process table become fakes, so a jailed open
// never reaches a real tmux server.
func stubOpenExecutor(t *testing.T, tmux action.TmuxClient) *openStub {
	t.Helper()
	stub := &openStub{}
	previous := newOpenExecutor
	t.Cleanup(func() { newOpenExecutor = previous })
	newOpenExecutor = func(dependencies action.Dependencies) (*action.Executor, error) {
		stub.heal = dependencies.Heal
		if dependencies.Heal != nil {
			dependencies.Heal = func(_ context.Context, threadID string) string {
				stub.healed = append(stub.healed, threadID)
				return ""
			}
		}
		dependencies.Tmux = tmux
		dependencies.Processes = emptyProcesses{}
		dependencies.Stderr = io.Discard
		return previous(dependencies)
	}
	return stub
}

// seedCodexThread writes a resumable Codex rollout into a jailed fleet's Codex
// home, the shape the Codex index source reads: a user-sourced session_meta
// header and one prompt. The credentials beside it are what make the home a
// configured Codex ACCOUNT — without them the loaded runtime carries no Codex
// root at all and the scan cannot see the thread.
func seedCodexThread(t *testing.T, root, id string) {
	t.Helper()
	credentials := filepath.Join(root, "codex", "auth.json")
	if err := os.WriteFile(
		credentials,
		[]byte(`{"tokens":{"access_token":"jailed","account_id":"jailed-account"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(root, "codex", "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-05-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"id":"` + id +
		`","thread_source":"user","cwd":"/work/codex-project"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user",` +
		`"content":[{"type":"input_text","text":"resume me"}]}}` + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOpenDetachedIDHealsACodexResume pins the repair the MCP door dropped: a
// Codex thread whose history projection is wedged resumes amnesiac unless the
// pre-resume heal runs, and the one moment it can run is before the seat
// exists. An executor built with empty dependencies carries no heal at all,
// and the resume reports success while the chat comes back blank.
func TestOpenDetachedIDHealsACodexResume(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "019f0000-0000-7000-8000-00000000000a"
	seedCodexThread(t, root, id)
	tmux := &fakeOpenTmux{alive: map[string]bool{}}
	stub := stubOpenExecutor(t, tmux)
	result, err := OpenDetachedID(context.Background(), id, io.Discard, nil)
	if err != nil {
		t.Fatalf("OpenDetachedID() error = %v", err)
	}
	if stub.heal == nil {
		t.Fatal("OpenDetachedID() built its executor with a nil Heal: a wedged Codex thread resumes amnesiac")
	}
	if len(stub.healed) != 1 || stub.healed[0] != id {
		t.Fatalf("heal calls = %v, want exactly one for %q", stub.healed, id)
	}
	if len(tmux.created) != 1 {
		t.Fatalf("spawn door called %d times, want 1: %#v", len(tmux.created), tmux.created)
	}
	if result.State != "opened" {
		t.Fatalf("result = %#v, want the resume reported as opened", result)
	}
}

// TestOpenDetachedIDFallsBackToHomeWhenCWDIsGone pins the substitution a
// daemon cannot make with os.Getwd(): the CLI opens a vanished directory's
// chat in the directory the operator is standing in, but the MCP server
// stands nowhere meaningful. A resumable row whose recorded directory is gone
// is born in the fleet home instead, and the result says so — a caller with
// no terminal sees nothing of the chat it started.
func TestOpenDetachedIDFallsBackToHomeWhenCWDIsGone(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "c3333333-3333-4333-8333-333333333333"
	seedClaudeChat(t, root, id)
	tmux := &fakeOpenTmux{alive: map[string]bool{}}
	stubOpenExecutor(t, tmux)
	result, err := OpenDetachedID(context.Background(), id, io.Discard, nil)
	if err != nil {
		t.Fatalf("OpenDetachedID() error = %v", err)
	}
	home := filepath.Join(root, "home")
	if len(tmux.created) != 1 {
		t.Fatalf("spawn door called %d times, want 1: %#v", len(tmux.created), tmux.created)
	}
	if tmux.created[0].CWD != home {
		t.Fatalf("chat server CWD = %q, want the fleet home %q", tmux.created[0].CWD, home)
	}
	if !strings.Contains(result.Detail, home) {
		t.Fatalf("result detail = %q, want it to name the directory the chat was opened in", result.Detail)
	}
}
