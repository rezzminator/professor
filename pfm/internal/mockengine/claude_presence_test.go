package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

func TestClaudePresenceDrivesLiveChatDeferral(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	path := filepath.Join(fix.configDir, "sessions", strconv.Itoa(os.Getpid())+".json")
	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &record); err != nil {
		t.Fatal(err)
	}
	if record["sessionId"] != fixtureSession || record["cwd"] != fix.work || record["pid"] != float64(os.Getpid()) ||
		record["kind"] != "interactive" {
		t.Fatalf(
			"%s: native identity=%v, want session=%s cwd=%s pid=%d kind=interactive",
			path,
			record,
			fixtureSession,
			fix.work,
			os.Getpid(),
		)
	}
	want := "account " + fix.configDir + " has live chats; close them and rerun pfm install --yes"
	if got, err := installer.LiveChatDeferral("/proc", fix.configDir); err != nil || got != want {
		t.Fatalf("native live deferral=%q err=%v, want %q", got, err, want)
	}
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s after exit: %v, want removed", path, err)
	}
	if got, err := installer.LiveChatDeferral("/proc", fix.configDir); err != nil || got != "" {
		t.Fatalf("ended deferral=%q err=%v, want empty", got, err)
	}
}
