package hookentry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestCaptureHookInputPreservesBytes(t *testing.T) {
	for _, payload := range []string{
		" {\"hook_event_name\":\"Notification\",\"notification_type\":\"permission_prompt\"}\n",
		`{"hook_event_name":"PermissionRequest","session_id":"thread-atlas","tool_name":"exec_command"}`,
		`{"session_id":"ses_atlas","message":"permission: bash"}`,
		" { broken JSON\n",
	} {
		t.Run(payload, func(t *testing.T) {
			directory := t.TempDir()
			env := &paths.MapEnv{Values: map[string]string{EnvHookCaptureDir: directory}}
			for range 2 {
				input, err := CaptureHookInput([]string{"herdr-state", "blocked"}, strings.NewReader(payload), env)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := io.ReadAll(input)
				if err != nil || string(actual) != payload {
					t.Fatalf("replayed = %q, %v; want %q", actual, err, payload)
				}
			}
			files, err := filepath.Glob(filepath.Join(directory, "*.json"))
			if err != nil || len(files) != 2 {
				t.Fatalf("%s: captured files = %v, %v; want two events", directory, files, err)
			}
			for _, path := range files {
				actual, err := os.ReadFile(path)
				if err != nil || string(actual) != payload {
					t.Fatalf("%s = %q, %v; want %q", path, actual, err, payload)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
				}
			}
		})
	}
}

func TestCaptureHookInputRefusals(t *testing.T) {
	for _, row := range []struct {
		name, directory, command string
		read                     bool
	}{
		{"disabled", "", "herdr-state", false},
		{"lifetime", t.TempDir(), "seat-host", false},
		{"watch worker", t.TempDir(), "herdr-state watch", false},
		{"tmux event", t.TempDir(), "herdr-state attach", false},
		{"registered hook", t.TempDir(), "launcher-repair", true},
		{"engine guard", t.TempDir(), "engine-guard", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			input := errorReader{}
			got, err := CaptureHookInput(
				strings.Fields(row.command),
				input,
				&paths.MapEnv{Values: map[string]string{EnvHookCaptureDir: row.directory}},
			)
			if row.read {
				want := "capture " + row.command + " hook input: read: unreadable input"
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
			} else if err != nil || got != input {
				t.Fatalf("reader = %v, %v; want unchanged reader", got, err)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("unreadable input") }
