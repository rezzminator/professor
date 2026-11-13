//go:build jail

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestChatLSPrintsNameBeforeSessionAtAndOverTruncationBoundary is the
// red-first proof for the NAME-primary row format: two real live tmux
// panes get distinct pane-title-derived names, one exactly at the 28-char
// truncation boundary and one well over it. For both rows the printed line
// must carry the (possibly truncated) NAME first, at a fixed column, with
// the tmux session id still present immediately after it — never lost,
// never reordered, and never pushed out of alignment by an oversized name.
func TestChatLSPrintsNameBeforeSessionAtAndOverTruncationBoundary(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	sidDir := filepath.Join(jail.root, "sid")

	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	pid := strconv.Itoa(os.Getpid())
	exactSocket := "cc-" + epoch + "-" + pid + "-1"
	overSocket := "cc-" + epoch + "-" + pid + "-2"
	exactName := strings.Repeat("A", 28) // exactly at the truncation boundary
	overName := strings.Repeat("B", 40)  // well over it

	seat := func(socket, title string) {
		startBootingPane(t, socket)
		paneID := strings.TrimSpace(runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_id}"))
		if output, err := exec.Command(
			"tmux", "-L", socket, "select-pane", "-t", paneID, "-T", title,
		).CombinedOutput(); err != nil {
			t.Fatalf("set pane title for %q: %v: %s", socket, err, output)
		}
		crumb := filepath.Join(sidDir, socket+"."+paneID)
		if err := os.WriteFile(crumb, []byte("/nonexistent/"+socket+".jsonl\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	seat(exactSocket, exactName)
	seat(overSocket, overName)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "ls", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls --all code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	findRow := func(socket string) string {
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(line, socket) {
				return line
			}
		}
		t.Fatalf("no row for socket %q in stdout=%q", socket, stdout.String())
		return ""
	}

	check := func(socket, fullName, wantTruncated string) {
		line := findRow(socket)
		const nameStart, nameWidth = 2, 28
		if len(line) < nameStart+nameWidth+1 {
			t.Fatalf("row for %q too short to hold the name column: %q", socket, line)
		}
		if got := line[nameStart : nameStart+nameWidth]; got != wantTruncated {
			t.Fatalf("row for %q name column = %q, want %q (line=%q)", socket, got, wantTruncated, line)
		}
		if line[nameStart+nameWidth] != ' ' {
			t.Fatalf("row for %q has no delimiter after the name column: %q", socket, line)
		}
		sessionStart := nameStart + nameWidth + 1
		if got := line[sessionStart : sessionStart+len(socket)]; got != socket {
			t.Fatalf(
				"row for %q session id at column %d = %q, want %q — name column pushed it out of alignment: %q",
				socket,
				sessionStart,
				got,
				socket,
				line,
			)
		}
		nameIdx := strings.Index(line, wantTruncated)
		sessionIdx := strings.Index(line, socket)
		if nameIdx < 0 || sessionIdx < 0 || nameIdx >= sessionIdx {
			t.Fatalf(
				"row for %q: name must appear before session id — name@%d session@%d line=%q",
				socket,
				nameIdx,
				sessionIdx,
				line,
			)
		}
		if len(fullName) > nameWidth && strings.Contains(stdout.String(), fullName) {
			t.Fatalf("full untruncated name %q leaked into stdout — truncation did not run", fullName)
		}
	}
	check(exactSocket, exactName, exactName)
	check(overSocket, overName, overName[:28])
}

// serveHerdrOneRequestPerConnection is a Herdr socket: one request line per
// connection, answered from the method's result in table.
func serveHerdrOneRequestPerConnection(t *testing.T, path string, table map[string]string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var asked struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if json.Unmarshal(line, &asked) != nil {
					return
				}
				_, _ = conn.Write([]byte(`{"id":"` + asked.ID + `","result":` + table[asked.Method] + "}\n"))
			}()
		}
	}()
}

// TestChatLSShowsEachChatsHerdrPresence pins the herdr column of `pfm chat
// ls` over two real tmux seats: one no Herdr client ever attached to (real
// tmux answers an unset option with nothing), one registered to a pane that
// moved (found by its terminal id over the real socket); and, with no Herdr on
// the host, a dash for every row. A failed lookup's text is presence_test's.
func TestChatLSShowsEachChatsHerdrPresence(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	sidDir := filepath.Join(jail.root, "sid")
	herdrSocket := filepath.Join(jail.root, "h.sock")
	serveHerdrOneRequestPerConnection(t, herdrSocket, map[string]string{
		"pane.list":      `{"type":"pane_list","panes":[{"pane_id":"w6:p2","terminal_id":"term_a","workspace_id":"w6"}]}`,
		"agent.list":     `{"type":"agent_list","agents":[{"pane_id":"w6:p2","workspace_id":"w6","agent":"claude","agent_status":"idle","name":"pfm-1"}]}`,
		"workspace.list": `{"type":"workspace_list","workspaces":[{"workspace_id":"w6","label":".professor"}]}`,
	})
	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	pid := strconv.Itoa(os.Getpid())
	seat := func(suffix string, clients map[string]herdr.Client) string {
		socket := "cc-" + epoch + "-" + pid + "-" + suffix
		startBootingPane(t, socket)
		paneID := strings.TrimSpace(runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_id}"))
		crumb := filepath.Join(sidDir, socket+"."+paneID)
		if err := os.WriteFile(crumb, []byte("/nonexistent/"+socket+".jsonl\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if clients != nil {
			stored, err := json.Marshal(herdr.Stored{Engine: "claude", State: "idle", Clients: clients})
			if err != nil {
				t.Fatal(err)
			}
			runTmuxOutput(t, socket, "set-option", "-t", socket, herdr.StoredOption, string(stored))
		}
		return socket
	}
	free := seat("1", nil)
	moved := seat("2", map[string]herdr.Client{"1": {Pane: "w1:p9", Terminal: "term_a", Socket: herdrSocket}})

	rowOf := func(stdout, socket string) string {
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, socket) {
				return line
			}
		}
		t.Fatalf("no row for socket %q in stdout=%q", socket, stdout)
		return ""
	}
	listAll := func() string {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"chat", "ls", "--all"}, &stdout, &stderr); code != 0 {
			t.Fatalf("chat ls --all code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	t.Setenv(herdr.EnvBin, "/usr/bin/herdr")
	withHerdr := listAll()
	if row := rowOf(withHerdr, free); !strings.Contains(row, herdr.PresenceNotInHerdr) {
		t.Errorf("row of a chat no Herdr client attached to = %q, want %q", row, herdr.PresenceNotInHerdr)
	}
	if row := rowOf(withHerdr, moved); !strings.Contains(row, "w6:p2 .professor idle pfm-1") {
		t.Errorf("row of a chat whose pane moved = %q, want its current pane, workspace label, status and name", row)
	}

	// No Herdr on the host: no HERDR_BIN_PATH, and a PATH that holds tmux alone.
	bin := filepath.Join(jail.root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmuxPath, filepath.Join(bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(herdr.EnvBin, "")
	t.Setenv("PATH", bin)
	without := listAll()
	for _, socket := range []string{free, moved} {
		if row := rowOf(without, socket); !strings.Contains(row, "  "+herdr.PresenceNoHerdr+"  ") {
			t.Errorf("row with no Herdr on the host = %q, want %q", row, herdr.PresenceNoHerdr)
		}
	}
}
