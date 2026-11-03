package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestClaudeCaptureSendsRecordedRequestsWithoutConsumingATurn(t *testing.T) {
	for _, alias := range []string{"sonnet", "opus"} {
		t.Run(alias, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{Steps: []Step{}})
			requests := make(chan []byte, 1)
			sink := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
					t.Errorf("capture request=%s %s, want POST /v1/messages", request.Method, request.URL.Path)
				}
				var err error
				actual, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- actual
				writer.WriteHeader(http.StatusBadRequest)
			}))
			defer sink.Close()
			t.Setenv("ANTHROPIC_BASE_URL", sink.URL)
			code, stdout, stderr := runOnce(
				fix,
				engineClaude,
				[]string{"-p", "x", "--model", alias, "--max-turns", "1", "--exclude-dynamic-system-prompt-sections"},
				"",
			)
			if code != ExitUsage || stdout != "" || stderr != "" {
				t.Fatalf("capture=(%d,%q,%q), want rejected sink (%d,empty,empty)", code, stdout, stderr, ExitUsage)
			}
			var actual []byte
			select {
			case actual = <-requests:
			case <-time.After(time.Second):
				t.Fatal("mock capture sent no request to the loopback sink")
			}
			golden := filepath.Join("testdata", "capture", alias+".json")
			expected, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(actual, &got); err != nil {
				t.Fatalf("%s: decode captured request %q: %v", golden, actual, err)
			}
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: captured=%s, want recorded request in golden", golden, actual)
			}
			if _, err := os.Stat(fix.scenario + cursorSuffix); !os.IsNotExist(err) {
				t.Fatalf("capture consumed a scripted turn: cursor=%s err=%v", fix.scenario+cursorSuffix, err)
			}
		})
	}
}

func TestClaudeCaptureRefusesUnscriptedRequests(t *testing.T) {
	for _, row := range []struct {
		name, base, model, prompt string
		args                      []string
		want                      string
	}{
		{"remote", "https://example.invalid", "sonnet", "x", []string{"--max-turns", "1"}, "mock Claude capture requires an HTTP loopback endpoint: https://example.invalid"},
		{"unknown model", "http://127.0.0.1:1", "haiku", "x", []string{"--max-turns", "1"}, `unscripted Claude capture model "haiku": record its request first`},
		{"unknown prompt", "http://127.0.0.1:1", "sonnet", "hello", []string{"--max-turns", "1"}, "unscripted Claude capture request: expected prompt x, max-turns 1 and the recorded system prompt"},
		{"unknown turns", "http://127.0.0.1:1", "sonnet", "x", []string{"--max-turns", "2"}, "unscripted Claude capture request: expected prompt x, max-turns 1 and the recorded system prompt"},
	} {
		t.Run(row.name, func(t *testing.T) {
			args := append([]string{"--exclude-dynamic-system-prompt-sections"}, row.args...)
			proc := &process{ctx: context.Background(), args: args, env: func(string) string { return row.base }}
			session := &claudeSession{proc: proc, model: row.model}
			handled, err := session.captureRequest(row.prompt)
			if !handled || err == nil || err.Error() != row.want {
				t.Fatalf("capture handled=%v err=%v, want handled error %q", handled, err, row.want)
			}
		})
	}
	proc := &process{ctx: context.Background(), env: func(string) string { return "" }}
	if handled, err := (&claudeSession{proc: proc}).captureRequest("ordinary prompt"); handled || err != nil {
		t.Fatalf("ordinary turn capture=(%v,%v), want unhandled", handled, err)
	}
	for _, status := range []int{http.StatusOK, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Location", "https://example.invalid")
				writer.WriteHeader(status)
			}))
			defer server.Close()
			proc := &process{
				ctx:  context.Background(),
				args: []string{"--exclude-dynamic-system-prompt-sections", "--max-turns=1"},
				env:  func(string) string { return server.URL },
			}
			handled, err := (&claudeSession{proc: proc, model: "sonnet"}).captureRequest("x")
			want := fmt.Sprintf("mock Claude capture expected HTTP 400, received %d", status)
			if !handled || err == nil || err.Error() != want {
				t.Fatalf("capture=(%v,%v), want handled error %q", handled, err, want)
			}
		})
	}
}
