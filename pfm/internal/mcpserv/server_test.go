package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

var (
	fleetBinaryOnce sync.Once
	fleetBinaryDir  string
	fleetBinaryPath string
	fleetBinaryErr  error
)

type protocolClient struct {
	clientSession *mcp.ClientSession
	serverSession *mcp.ServerSession
}

func connectInMemory(t *testing.T, server *mcp.Server) protocolClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "pfm-test",
		Version: "test",
	}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return protocolClient{
		clientSession: clientSession,
		serverSession: serverSession,
	}
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name string, arguments any) T {
	t.Helper()
	// One bound covers every call here, and the heaviest is not close to the
	// others: the oversize-paste case drives a full megabyte through bracketed
	// paste into a real jailed tmux pane and back. 15s fit that on an idle box
	// and not under `go test ./...`, where this package competes with every
	// other for exec and scheduler time, so a healthy megabyte round trip
	// reported itself as a deadline. Size the bound for the heaviest legitimate
	// operation; it is only ever paid in full when a call genuinely hangs.
	const callBound = 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), callBound)
	defer cancel()
	started := time.Now()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		// Name how long it actually waited: a call that died at the bound is a
		// different problem from one that failed immediately, and "context
		// deadline exceeded" alone does not say which happened.
		t.Fatalf("%s: after %s of a %s bound: %v", name, time.Since(started).Round(time.Millisecond), callBound, err)
	}
	if result.IsError {
		t.Fatalf("%s returned tool error: %#v", name, result.Content)
	}
	content, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output T
	if err := json.Unmarshal(content, &output); err != nil {
		t.Fatalf("%s structured output %s: %v", name, content, err)
	}
	return output
}

func captureInt(value int) *int { return &value }

type resolveGateCaller struct {
	target pfmcall.Target
	code   int    // the call engine's resolve code, 0 for one target
	detail string // its candidate detail
}

func (fake resolveGateCaller) Resolve(context.Context, string) (pfmcall.Target, int, string, error) {
	return fake.target, fake.code, fake.detail, nil
}

func (fake resolveGateCaller) ResolveEngine(_ context.Context, _, engine string) (pfmcall.Target, int, string, error) {
	if engine != string(pfmengine.Codex) {
		return pfmcall.Target{}, pfmcall.CodeUnknown, "", nil
	}
	return fake.target, 0, "", nil
}

func (resolveGateCaller) Capture(context.Context, string, int) (pfmcall.Target, string, int, string, error) {
	return pfmcall.Target{}, "", 0, "", nil
}

func (resolveGateCaller) Call(context.Context, pfmcall.Request) (pfmcall.Result, error) {
	return pfmcall.Result{}, nil
}

func TestChatResolveEveryKindUsesTheCallResolutionGate(t *testing.T) {
	t.Setenv("PFM_TMUX_DIR", t.TempDir())
	t.Setenv("PFM_SID_DIR", t.TempDir())
	raw, err := resolve.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := pfmcall.Target{SocketPath: "/tmp/tmux-1000/cx-1-2-3", Pane: "%7", Engine: "cx"}
	service := newService("test", &backend{
		resolver:   *raw,
		callEngine: resolveGateCaller{target: want},
	})
	protocol := connectInMemory(t, service.Server())
	for _, kind := range []string{"label", "session", "cxwin"} {
		resolved := callTool[ResolveOutput](t, protocol.clientSession, "resolve", ResolveInput{
			Kind: kind, Name: "same name",
		})
		if resolved.Status != "ok" || resolved.Code != 0 ||
			resolved.SocketPath != want.SocketPath || resolved.Pane != want.Pane {
			t.Fatalf("resolve kind %s=%+v, want call gate target %+v", kind, resolved, want)
		}
	}
	// Several chats answering the name are chat.ResolveRefusal's ambiguous refusal, the CLI's exit and wording.
	ambiguous := newService("test", &backend{
		resolver:   *raw,
		callEngine: resolveGateCaller{code: pfmcall.CodeAmbiguous, detail: "twin-a twin-b"},
	})
	resolved := callTool[ResolveOutput](t, connectInMemory(t, ambiguous.Server()).clientSession, "resolve",
		ResolveInput{Kind: "label", Name: "twin"})
	wantOut := ResolveOutput{Status: "ambiguous", Code: 2, Message: "twin-a twin-b", Candidates: "twin-a twin-b"}
	if resolved != wantOut {
		t.Fatalf("resolve of an ambiguous name = %+v, want %+v", resolved, wantOut)
	}
}

// setupBackendPaths lays the temp-dir paths a backend test reads (home, roots,
// session ids, tmux, proc, the cache database the SQLite door serves from
// memory) and points the environment at them.
func setupBackendPaths(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	claude := filepath.Join(root, "claude")
	codex := filepath.Join(root, "codex")
	sid := filepath.Join(root, "sid")
	proc := filepath.Join(root, "proc")
	tmux := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	for _, directory := range []string{
		home, claude, codex, sid, proc, tmux,
		filepath.Join(claude, "project-alpha"),
		filepath.Join(claude, "project-beta"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONL(t, filepath.Join(claude, "project-alpha", "alpha.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": testjail.Place("/work/alpha"),
			"message": map[string]any{"content": "alpha unique prompt"},
		},
		map[string]any{
			"type": "assistant",
			"message": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "alpha answer"},
			}},
		},
	})
	writeJSONL(t, filepath.Join(claude, "project-beta", "beta.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": testjail.Place("/work/beta"),
			"message": map[string]any{"content": "beta unique prompt"},
		},
		map[string]any{
			"type":    "assistant",
			"message": map[string]any{"content": "beta answer"},
		},
	})
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvSIDDir, sid)
	t.Setenv(paths.EnvClaudeRoots, claude)
	t.Setenv(paths.EnvCodexHome, codex)
	t.Setenv(paths.EnvTmuxDir, tmux)
	t.Setenv(paths.EnvProcRoot, proc)
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "state", "pfm-cache.db"))
	return root
}

func writeJSONL(t *testing.T, path string, records []any) {
	t.Helper()
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChatKeysMCPRejectsUnknownNamesBeforeResolution(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", nil, Runtime{Store: memoryStore(t), Paths: resolved})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	protocol := connectInMemory(t, service.Server())
	result, err := protocol.clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "keys",
		Arguments: KeysInput{Target: "not-used", Keys: []string{"Esc"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprintf(
		"error=%t %s",
		result.IsError,
		content,
	), "error=true [{\"type\":\"text\",\"text\":\"\\\"Esc\\\" is not a tmux key — tmux would type it as text; send it literal (--literal, MCP literal=true) to mean that, or use one of: BSpace BTab DC Delete Down End Enter Escape Home IC Insert Left NPage PPage PageDown PageUp PgDn PgUp Right Space Tab Up F1-F12, each optionally prefixed C- M- S-\"}]"; got != want {
		t.Fatalf("unknown key result = %q, want %q", got, want)
	}
}

type stdioJail struct {
	root            string
	home            string
	claude          string
	codex           string
	sid             string
	proc            string
	tmuxBase        string
	tmuxDir         string
	database        string
	socket          string
	session         string
	pane            string
	transcript      string
	selectorSocket  string
	selectorSession string
	busySocket      string
	busySession     string
	codexSocket     string
	codexSession    string
	sockets         []string
}

func (jail *stdioJail) startUI(t *testing.T, socket, session, script, mode, label, marker, initial string) {
	t.Helper()
	command := exec.Command(
		"tmux",
		"-f", "/dev/null",
		"-L", socket,
		"new-session",
		"-d",
		"-s", session,
		"-n", "fixture",
		"python3", script, mode, label, marker, initial,
	)
	command.Env = jail.environment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed tmux %q: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)
}

func (jail *stdioJail) environment() []string {
	return append(os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.tmuxBase,
		paths.EnvHome+"="+jail.home,
		paths.EnvCacheDB+"="+jail.database,
		paths.EnvSIDDir+"="+jail.sid,
		paths.EnvClaudeRoots+"="+jail.claude,
		paths.EnvCodexHome+"="+jail.codex,
		paths.EnvTmuxDir+"="+jail.tmuxDir,
		paths.EnvProcRoot+"="+jail.proc,
		// A jailed daemon has no tmux server, no session id and no ancestry to
		// recover a handle from, so it derives NO sender — and pfm refuses to
		// deliver an unsigned message rather than hand the recipient an
		// instruction from nobody. Stating the sender is the supported answer
		// for exactly this shape (a detached or scrubbed caller), so the
		// fixture states one instead of disabling the guard it shares a code
		// path with.
		pfmcall.SenderSessionEnv+"=cc-1700000000-1-1",
		pfmcall.SenderLabelEnv+"=mcp-jail-fixture",
		pfmcall.SenderIDEnv+"=00000000-0000-4000-8000-000000000000",
		"CHAT_CALL_POLL=0.01",
		"CHAT_CALL_ENTER_SETTLE=0.02",
		"CHAT_CALL_PROOF_SETTLE=0.02",
	)
}

// The chat server's routing text, byte for byte: every clause names a tool it registers.
func TestChatInstructionsPinTheRoutingText(t *testing.T) {
	want := "Message a chat → call; list chats → ls; who am I → whoami; start a chat or model run → new, never claude -p or pfm headless exec; kill a run once done, exit this chat; fork this conversation → pfm chat branch; is a chat idle or busy → status; its last answer → last; old transcripts → /chatlog; save my transcript → save. Chats are sessions, never sub-agents. end, modal, watch, stream, recover and history stay shell-only pfm chat commands."
	if chatInstructions != want {
		t.Fatalf("chat server Instructions =\n%q\nwant\n%q", chatInstructions, want)
	}
}
