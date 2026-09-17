//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeHarnessCaptureFixture(t *testing.T) {
	if os.Getenv("PFM_E2E_CLAUDE_CAPTURE") != "1" {
		t.Skip("Claude harness capture fixture runs only as a helper process")
	}
	home := os.Getenv("PFM_E2E_HOME")
	if home == "" || home != os.Getenv("HOME") {
		t.Fatal("capture fixture requires its private HOME")
	}
	endpoint, err := url.Parse(os.Getenv("ANTHROPIC_BASE_URL"))
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" {
		t.Fatal("capture fixture refuses a non-loopback endpoint")
	}
	alias := ""
	for index, arg := range os.Args {
		if arg == "--model" && index+1 < len(os.Args) {
			alias = os.Args[index+1]
		}
	}
	stem := "harness-original"
	switch alias {
	case "sonnet":
	case "opus":
		stem = "harness-opus"
	default:
		t.Fatalf("unexpected capture model %q", alias)
	}
	dir := filepath.Join(home, ".local", "share", "pfm", "install", "prompts")
	pin, err := os.ReadFile(filepath.Join(dir, stem+".sha256"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pin))
	if len(fields) != 2 || filepath.Base(fields[1]) != fields[1] {
		t.Fatal("malformed fixture baseline pin")
	}
	prompt, err := os.ReadFile(filepath.Join(dir, fields[1]))
	if err != nil {
		t.Fatal(err)
	}
	model, err := os.ReadFile(filepath.Join(dir, stem+".model"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(
		map[string]any{"model": strings.TrimSpace(string(model)), "system": strings.TrimSuffix(string(prompt), "\n")},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Post(endpoint.String()+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response.Body: %v", err)
		}
	}()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("sink status=%d", response.StatusCode)
	}
}
