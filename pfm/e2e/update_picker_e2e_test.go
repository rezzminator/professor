//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestOlderPFMDiscoversUpdateThenPickerLaunchesGuidedEngine(t *testing.T) {
	requireE2EFence(t)
	for _, binary := range []string{"go", "tmux", "zsh"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("update-picker e2e needs %s: %v", binary, err)
		}
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	professor := filepath.Join(home, ".professor")
	binDir := filepath.Join(root, "bin")
	configDir := filepath.Join(home, ".cc", "1")
	codexHome := filepath.Join(home, ".codex")
	opencodeHome := filepath.Join(home, ".local", "share", "opencode")
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	state := filepath.Join(root, "state")
	// Darwin limits Unix-domain socket paths to 104 bytes. Go's testing temp
	// directory is already long there, so keep tmux's socket root explicitly
	// short while leaving every other fixture inside the test jail.
	tmuxBase, err := os.MkdirTemp("/tmp", "pfm-update-tmux-")
	if err != nil {
		t.Fatalf("create short tmux socket root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmuxBase); err != nil {
			t.Errorf("remove short tmux socket root: %v", err)
		}
	})
	tmuxDir := filepath.Join(tmuxBase, "tmux-"+strconv.Itoa(os.Getuid()))
	for _, dir := range []string{
		home, professor, binDir, configDir, codexHome, opencodeHome, managed, state, tmuxDir,
		filepath.Join(home, ".config", "pfm"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opencodeHome, "opencode.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture-token","account_id":"fixture-account"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "source-repo"), []byte(professor+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "pfm", "config.json")
	configuration := map[string]any{
		"version": pfmconfig.Version,
		"accounts": []map[string]any{{
			"id": 1, "configDir": configDir,
		}},
		"codex": map[string]any{
			"homes": []map[string]any{{"id": 1, "home": codexHome}},
		},
		"ask": map[string]any{"engine": "claude"},
	}
	configRaw, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot := filepath.Dir(packageDir)
	oldPFM := filepath.Join(binDir, "pfm")
	build := exec.Command(
		"go", "-C", moduleRoot, "build",
		"-ldflags", "-X main.version=v0.61.1",
		"-o", oldPFM, "./cmd/pfm",
	)
	build.Env = replaceUpdateE2EEnv(os.Environ(), map[string]string{"GOFLAGS": "-buildvcs=false"})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build older pfm: %v: %s", err, output)
	}

	proof := filepath.Join(root, "engine-proof")
	fakeCodex := "#!/bin/sh\n" +
		"{ printf 'cwd=%s\\n' \"$PWD\"; printf 'args=%s\\n' \"$*\"; } > \"$PFM_UPDATE_LAUNCH_PROOF\"\n" +
		"sleep 2\n"
	if err := os.WriteFile(filepath.Join(binDir, "cx"), []byte(fakeCodex), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"opencode"} {
		stub := "#!/bin/sh\nprintf 'unexpected engine=" + engine + "\\n' > \"$PFM_UPDATE_LAUNCH_PROOF\"\nsleep 2\n"
		if err := os.WriteFile(filepath.Join(binDir, engine), []byte(stub), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	lookupStarted := make(chan struct{}, 1)
	releaseLookup := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		select {
		case lookupStarted <- struct{}{}:
		default:
		}
		<-releaseLookup
		writer.Header().Set("Location", "/rezzminator/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	lookupReleased := false
	defer func() {
		if !lookupReleased {
			close(releaseLookup)
		}
	}()

	environment := replaceUpdateE2EEnv(os.Environ(), map[string]string{
		"HOME":                    home,
		"PATH":                    binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM":                    "xterm-256color",
		"TMUX":                    "",
		"TMUX_TMPDIR":             tmuxBase,
		"PFM_HOME":                home,
		"PFM_DB":                  filepath.Join(state, "fleet.db"),
		"PFM_FLEET_DB":            filepath.Join(state, "shared.db"),
		"PFM_SID_DIR":             filepath.Join(root, "sid"),
		"PFM_CLAUDE_ROOTS":        filepath.Join(root, "claude"),
		"PFM_CODEX_ROOT":          codexHome,
		"PFM_OPENCODE_ROOT":       opencodeHome,
		"PFM_TMUX_DIR":            tmuxDir,
		"PFM_PROC_ROOT":           filepath.Join(root, "proc"),
		"PFM_CGROUP_ROOT":         filepath.Join(root, "cgroup"),
		"PFM_TMUX_CONF":           "/dev/null",
		"PFM_UPDATE_LATEST_URL":   server.URL,
		"PFM_UPDATE_LAUNCH_PROOF": proof,
	})
	for _, dir := range []string{
		filepath.Join(root, "sid"), filepath.Join(root, "claude"),
		filepath.Join(root, "proc"), filepath.Join(root, "cgroup"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	firstSocket := "update-first-" + strconv.Itoa(os.Getpid())
	secondSocket := "update-second-" + strconv.Itoa(os.Getpid())
	for _, socket := range []string{firstSocket, secondSocket} {
		socket := socket
		t.Cleanup(func() {
			command := exec.Command("tmux", "-L", socket, "kill-server")
			command.Env = environment
			_ = command.Run()
		})
	}
	startUpdatePickerE2E(t, firstSocket, oldPFM, environment)
	first := waitForUpdatePickerScreen(t, firstSocket, environment, func(screen string) bool {
		return strings.Contains(screen, "pfm") && strings.Contains(screen, "Claude")
	})
	if strings.Contains(first, "PROFESSOR UPDATE") {
		t.Fatalf("first invocation consumed its own background result:\n%s", first)
	}
	select {
	case <-lookupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("detached update lookup never reached the controlled release server")
	}
	if err := sendUpdatePickerKey(firstSocket, environment, "Escape"); err != nil {
		t.Fatal(err)
	}
	close(releaseLookup)
	lookupReleased = true
	cache := filepath.Join(state, "update-check.json")
	if !waitForUpdateE2EFile(cache, 10*time.Second) {
		t.Fatal("background update lookup did not persist the next-invocation cache")
	}

	startUpdatePickerE2E(t, secondSocket, oldPFM, environment)
	second := waitForUpdatePickerScreen(t, secondSocket, environment, func(screen string) bool {
		return strings.Contains(screen, "PROFESSOR UPDATE") &&
			strings.Contains(screen, "v0.61.2") &&
			strings.Contains(screen, "Claude") &&
			strings.Contains(screen, "Codex") &&
			strings.Contains(screen, "OpenCode")
	})
	if strings.Index(second, "PROFESSOR UPDATE") > strings.Index(second, "New") && strings.Contains(second, "New") {
		t.Fatalf("update banner did not lead the new-chat row:\n%s", second)
	}
	if err := sendUpdatePickerKey(secondSocket, environment, "Right"); err != nil {
		t.Fatal(err)
	}
	selectedCodex := waitForUpdatePickerScreen(t, secondSocket, environment, func(screen string) bool {
		return strings.Contains(screen, "◖ Codex ◗")
	})
	if !strings.Contains(selectedCodex, "[ Claude ]") || !strings.Contains(selectedCodex, "[ OpenCode ]") {
		t.Fatalf("engine carousel after Right is incomplete:\n%s", selectedCodex)
	}
	if err := sendUpdatePickerKey(secondSocket, environment, "Enter"); err != nil {
		t.Fatal(err)
	}
	if !waitForUpdateE2EFile(proof, 10*time.Second) {
		t.Fatal("clicking the update banner did not launch the selected engine")
	}
	launched, err := os.ReadFile(proof)
	if err != nil {
		t.Fatal(err)
	}
	launch := string(launched)
	for _, want := range []string{
		"cwd=" + professor,
		"Professor v0.61.2 is available",
		"present a concise overview",
		"Ask the user for explicit approval",
		"pfm update --to v0.61.2",
	} {
		if !strings.Contains(launch, want) {
			t.Fatalf("launched Codex proof %q lacks %q", launch, want)
		}
	}
	t.Logf(
		"UPDATE_PICKER first-run-nonblocking=true next-run-banner=true selected=Codex cwd=%s approval-first=true",
		professor,
	)
}

func startUpdatePickerE2E(t *testing.T, socket, pfm string, environment []string) {
	t.Helper()
	commandLine := updateE2EShellQuote(pfm) + " ls --no-sky; " +
		"rc=$?; printf '\\nPFM_RC=%s\\n' \"$rc\"; sleep 60"
	command := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-x", "150", "-y", "30", "-s", "picker", commandLine,
	)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start update picker: %v: %s", err, output)
	}
}

func waitForUpdatePickerScreen(t *testing.T, socket string, environment []string, ready func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		command := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-J", "-S", "-200", "-t", "picker")
		command.Env = environment
		output, err := command.Output()
		if err == nil {
			last = string(output)
			if ready(last) {
				return last
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("picker screen did not reach expected state; last:\n%s", last)
	return ""
}

func sendUpdatePickerKey(socket string, environment []string, key string) error {
	command := exec.Command("tmux", "-L", socket, "send-keys", "-t", "picker", key)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("send %s: %w: %s", key, err, output)
	}
	return nil
}

func waitForUpdateE2EFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() != 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func replaceUpdateE2EEnv(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := values[name]; replaced {
				continue
			}
		}
		result = append(result, entry)
	}
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

func updateE2EShellQuote(value string) string {
	var output bytes.Buffer
	output.WriteByte('\'')
	output.WriteString(strings.ReplaceAll(value, "'", "'\\''"))
	output.WriteByte('\'')
	return output.String()
}
