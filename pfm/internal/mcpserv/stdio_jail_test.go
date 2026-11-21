//go:build jail

package mcpserv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// With no daemon, the in-process server resolves a Codex caller by its
// _meta.threadId, exactly as the daemon does.
func TestRunStdioResolvesCodexCallerInProcess(t *testing.T) {
	service := metadataIdentityService(t)
	probe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := proxyTestAddress(probe)
	probe.Close()

	var warnings proxyTestBuffer
	session := stdioTestSession(t, stdioTestProfessor(t, service), StdioOptions{
		DaemonAddress: address, Home: service.backend.paths.Home,
		SIDDir: service.backend.paths.SIDDir, Warnings: &warnings,
	})
	whoami := callToolWithMeta[WhoamiOutput](
		t, session, "whoami", mcp.Meta{"threadId": "thread-a"}, WhoamiInput{},
	)
	if whoami.Status != "ok" || whoami.ID != "thread-a" || whoami.Session != "fixture-session" ||
		whoami.Engine != string(pfmengine.Codex) {
		t.Fatalf("in-process whoami = %+v, want thread-a on fixture-session", whoami)
	}
	if !strings.Contains(warnings.String(), "daemon absent at "+address) {
		t.Fatalf("warnings = %q, want the absent-daemon fallback", warnings.String())
	}
}

func TestRunStdioUsesHealthyDaemonProxy(t *testing.T) {
	var daemonWarnings, localWarnings bytes.Buffer
	daemon := stdioTestService("daemon", &daemonWarnings)
	server := httptest.NewServer(proxyTestDaemon(daemon))
	defer server.Close()

	local := stdioTestService("local", &localWarnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local.backend.paths = resolved
	address := proxyTestAddress(server)
	markerPath := filepath.Join(local.backend.paths.Home, ".local", "state", "pfm", "mcp-proxy-compatible")
	inspect := func() {
		markerTarget, err := filepath.EvalSymlinks(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		links, err := gather.NewProcFS("").FDLinks(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			if link.Target == markerTarget {
				return
			}
		}
		t.Fatalf("healthy daemon proxy does not hold marker %s", markerPath)
	}
	if marker := stdioTestRunInspect(t, local, address, inspect).Message; marker != "daemon" {
		t.Fatalf("healthy daemon marker = %q, want proxy daemon marker", marker)
	}
	links, err := gather.NewProcFS("").FDLinks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	markerTarget, err := filepath.EvalSymlinks(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Target == markerTarget {
			t.Fatalf("proxy marker descriptor %d remained open after proxy returned", link.FD)
		}
	}
}

// handoverJailClient is a chat's engine in a tmux pane: it drops every
// environment variable that would name its caller, so the stdio server can
// only find it by walking its process chain, starts pfm's stdio server,
// handshakes, asks whoami, waits for the test's install, asks again and
// reports both answers.
const handoverJailClient = `import json, os, subprocess, sys, time
binary, config, report, installed, release = sys.argv[1:6]
strip = set(sys.argv[6:])
env = {k: v for k, v in os.environ.items() if k not in strip}
stderr = open(report + ".stderr", "w")
server = subprocess.Popen([binary, "--config", config, "mcp", "serve", "--stdio", "--family", "chat"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr, env=env)
def send(frame):
    server.stdin.write((json.dumps(frame) + "\n").encode()); server.stdin.flush()
def answer(id):
    while True:
        line = server.stdout.readline()
        if not line:
            raise SystemExit("server ended before answering %d" % id)
        frame = json.loads(line)
        if frame.get("id") == id:
            return frame
def wait_for(path):
    for _ in range(2000):
        if os.path.exists(path):
            return
        time.sleep(0.01)
    raise SystemExit("never saw " + path)
def whoami(id):
    send({"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": {"name": "whoami", "arguments": {}}})
    return answer(id)["result"]["structuredContent"]
send({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18",
    "capabilities": {}, "clientInfo": {"name": "handover-jail", "version": "test"}}})
answer(1)
send({"jsonrpc": "2.0", "method": "notifications/initialized"})
first = whoami(2)
open(report + ".first", "w").write(json.dumps({"pid": server.pid, "pane": os.environ["TMUX_PANE"], "whoami": first}))
wait_for(installed)
second = whoami(3)
open(report + ".second", "w").write(json.dumps({"whoami": second}))
wait_for(release)
server.stdin.close()
open(report + ".exit", "w").write(str(server.wait()))
`

// TestStdioHandoverMovesALiveChatOntoTheInstalledBuild is the stale stdio
// server end to end: a chat's server runs a pfm build an install then renames
// a new build over, and the chat's next tools/call is answered by the new
// build, which finds the same caller through the extra pfm in its ancestry.
func TestStdioHandoverMovesALiveChatOntoTheInstalledBuild(t *testing.T) {
	for _, tool := range []string{"tmux", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required: %v", tool, err)
		}
	}
	jail := newStdioJail(t)
	installed := filepath.Join(jail.root, "bin", "pfm")
	copyHandoverBuild(t, buildFleetBinary(t), installed)
	client := filepath.Join(jail.root, "handover-client.py")
	if err := os.WriteFile(client, []byte(handoverJailClient), 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(jail.root, "handover")
	installDone, release := report+".installed", report+".release"
	// The client takes over the fixture chat's own pane, the one its fleet row names.
	command := exec.Command("tmux", "-L", jail.socket, "respawn-pane", "-k", "-t", jail.pane,
		"python3", client, installed, writeEnabledMCPConfig(t, jail.root), report, installDone, release,
		"TMUX", "TMUX_PANE", paths.EnvProcRoot,
		pfmcall.SenderSessionEnv, pfmcall.SenderLabelEnv, pfmcall.SenderIDEnv)
	command.Env = jail.environment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start the chat's pane: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	var first struct {
		PID    int          `json:"pid"`
		Pane   string       `json:"pane"`
		Whoami WhoamiOutput `json:"whoami"`
	}
	readHandoverReport(t, report, ".first", &first)
	wantWhoami := WhoamiOutput{
		Status: "ok", Session: jail.session, SocketPath: filepath.Join(jail.tmuxDir, jail.socket),
		SocketName: jail.socket, Pane: jail.pane, Engine: "cc", ID: "fixture", Source: "mcp-proxy",
	}
	if first.Whoami != wantWhoami {
		t.Fatalf("whoami before the install = %+v, want %+v", first.Whoami, wantWhoami)
	}
	copyHandoverBuild(t, buildFleetBinary(t), installed+".new")
	if err := os.Rename(installed+".new", installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installDone, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var second struct {
		Whoami WhoamiOutput `json:"whoami"`
	}
	readHandoverReport(t, report, ".second", &second)
	proc := gather.NewProcFS("")
	children := processChildren(t, proc, first.PID)
	if second.Whoami != wantWhoami || len(children) != 1 {
		t.Fatalf("whoami after the install = %+v with server children %v, want %+v from one child; server stderr %s",
			second.Whoami, children, wantWhoami, report+".stderr")
	}
	child := children[0]
	stat, parentErr := proc.Stat(child)
	parent := stat.ParentPID
	running, imageErr := proc.(gather.ProcImage).Image(child)
	want, wantErr := gather.FileIDOf(installed)
	if parentErr != nil || imageErr != nil || wantErr != nil || parent != first.PID || running != want {
		t.Fatalf("server child %d: parent %d (%v), image %+v (%v); want parent %d running the installed %s %+v (%v)",
			child, parent, parentErr, running, imageErr, first.PID, installed, want, wantErr)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var exit int
	readHandoverReport(t, report, ".exit", &exit)
	if exit != 0 {
		stderr, _ := os.ReadFile(report + ".stderr")
		t.Fatalf("server exit = %d, want 0; stderr %s:\n%s", exit, report+".stderr", stderr)
	}
}

// processChildren is every process whose parent is pid, read from the process
// table (a /proc task's children file names only one thread's).
func processChildren(t *testing.T, proc gather.ProcFS, pid int) []int {
	t.Helper()
	pids, err := proc.PIDs()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	var children []int
	for _, candidate := range pids {
		if stat, err := proc.Stat(candidate); err == nil && stat.ParentPID == pid {
			children = append(children, candidate)
		}
	}
	return children
}

// readHandoverReport waits for the client's report file and decodes it.
func readHandoverReport(t *testing.T, report, suffix string, into any) {
	t.Helper()
	path := report + suffix
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, err := os.ReadFile(path)
		if err == nil && len(body) != 0 {
			if err := json.Unmarshal(body, into); err != nil {
				t.Fatalf("decode %s: %v: %s", path, err, body)
			}
			return
		}
		if time.Now().After(deadline) {
			stderr, _ := os.ReadFile(report + ".stderr")
			t.Fatalf("no report at %s within 30s (%v); server stderr %s:\n%s", path, err, report+".stderr",
				strings.TrimSpace(string(stderr)))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// copyHandoverBuild copies a pfm build to path, as an install stages one.
func copyHandoverBuild(t *testing.T, source, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	from, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(to, from); err != nil {
		_ = to.Close()
		t.Fatal(err)
	}
	if err := to.Close(); err != nil {
		t.Fatalf("close the build copy %s: %v", path, err)
	}
}
