package codexappendix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNativeHookDelivery is opt-in because the native executable is not a Go
// dependency. The HTTP sink captures the assembled request and rejects it;
// no model inference or credentials are used.
func TestNativeHookDelivery(t *testing.T) {
	native := os.Getenv("PFM_TEST_NATIVE_CODEX")
	pfm := os.Getenv("PFM_TEST_APPENDIX_PFM")
	if native == "" || pfm == "" {
		t.Skip("native hook contract requires isolated native/PFM executables")
	}
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	project := filepath.Join(home, "project")
	for _, dir := range []string{account, filepath.Join(home, ".local", "bin"), filepath.Dir(PromptPath(home)), filepath.Join(project, ".codex"), filepath.Join(project, ".git")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(pfm, filepath.Join(home, ".local", "bin", "pfm")); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(PromptPath(home), "NATIVE_APPENDIX_PROOF")
	definition := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": Matcher,
					"hooks": []any{
						map[string]any{"type": "command", "command": Command(home), "timeout": 10},
						map[string]any{"type": "command", "command": "printf PERSONAL_UNTRUSTED"},
					},
				},
			},
		},
	}
	encoded, _ := json.Marshal(definition)
	write(filepath.Join(account, "hooks.json"), string(encoded))
	requests := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := r.Body.Close(); err != nil {
				t.Errorf("close r.Body: %v", err)
			}
		}()
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			t.Error(err)
		}
		var request map[string]any
		if err := json.Unmarshal(raw, &request); err == nil {
			requests <- request
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"intentional request capture","type":"invalid_request_error"}}`))
	}))
	defer server.Close()
	config := fmt.Sprintf(
		"model='gpt-6-astra'\nmodel_provider='capture'\ndeveloper_instructions='PERSONAL_NATIVE'\n[model_providers.capture]\nname='capture'\nbase_url=%q\nwire_api='responses'\nrequires_openai_auth=false\n[projects.%q]\ntrust_level='trusted'\n",
		server.URL,
		project,
	)
	write(filepath.Join(account, "config.toml"), config)
	write(filepath.Join(project, ".codex", "config.toml"), "developer_instructions='PROJECT_NATIVE'\n")
	t.Setenv("HOME", home)
	t.Setenv("PFM_HOME", home)
	t.Setenv("CODEX_HOME", account)
	if err := RegisterAppendix(context.Background(), native, home, account, false); err != nil {
		t.Fatal(err)
	}
	raw, err := rpc(context.Background(), native, account, "hooks/list", map[string]any{"cwds": []string{project}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"trustStatus":"trusted"`) ||
		!strings.Contains(string(raw), `"trustStatus":"untrusted"`) {
		t.Fatalf("individual trust failed: %s", raw)
	}

	capture := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, native, args...)
		output, runErr := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("native capture timed out: %s", output)
		}
		if runErr == nil {
			t.Fatal("HTTP sink should have rejected inference")
		}
		select {
		case request := <-requests:
			raw, _ := json.Marshal(request)
			if !strings.Contains(string(raw), "PROJECT_NATIVE") ||
				strings.Count(string(raw), "NATIVE_APPENDIX_PROOF") != 1 {
				t.Fatalf("missing/duplicated developer contexts: %s\n%s", raw, output)
			}
			if !strings.Contains(string(raw), "You are Codex") {
				t.Fatal("native model base missing")
			}
		case <-time.After(time.Second):
			t.Fatalf("no native request captured: %s", output)
		}
		return output
	}
	output := capture("-C", project, "exec", "--skip-git-repo-check", "--json", "Say capture.")
	var threadID string
	for _, line := range strings.Split(string(output), "\n") {
		var event struct {
			Type string `json:"type"`
			ID   string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "thread.started" {
			threadID = event.ID
		}
	}
	if threadID == "" {
		t.Fatalf("native thread ID absent: %s", output)
	}
	capture("-C", project, "exec", "resume", "--skip-git-repo-check", "--json", threadID, "Resume capture.")
	capture(
		"-C",
		project,
		"--model",
		"gpt-5.6-luna",
		"exec",
		"--skip-git-repo-check",
		"--json",
		"Second model capture.",
	)
	if err := RegisterAppendix(context.Background(), native, home, account, true); err != nil {
		t.Fatal(err)
	}
	raw, err = rpc(context.Background(), native, account, "hooks/list", map[string]any{"cwds": []string{project}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"trustStatus":"trusted"`) {
		t.Fatalf("uninstall retained owned trust: %s", raw)
	}
}
