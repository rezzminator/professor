package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiCatch is a RoundTripper standing in for a Messages endpoint: it records each request's URL and
// body and answers with its status, or fails the round trip when status is 0.
type apiCatch struct {
	status int
	urls   []string
	bodies []apiRequest
}

func (catch *apiCatch) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var decoded apiRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	catch.urls = append(catch.urls, request.URL.String()+" key="+request.Header.Get("X-Api-Key"))
	catch.bodies = append(catch.bodies, decoded)
	if catch.status == 0 {
		return nil, errors.New("connection refused")
	}
	return &http.Response{
		StatusCode: catch.status, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"message":"captured"}}`)),
	}, nil
}

// TestClaudeHeadlessAsksItsBaseURL pins what a headless Claude sends ANTHROPIC_BASE_URL: the settings
// env blocks override the process environment (project local last), a refused or unreachable endpoint
// is the turn's API error, the system prompt carries a remote experiment line bucketed per config dir
// unless CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC is 1, and with no base URL nothing is sent.
func TestClaudeHeadlessAsksItsBaseURL(t *testing.T) {
	const instructions = claudeHarnessInstructions + "\n\n# Experiments\n - Experiment bucket "
	cases := []struct {
		name         string
		env          map[string]string
		project      string // .claude/settings.json
		local        string // .claude/settings.local.json
		status       int
		wantURLs     []string
		wantStdout   string
		wantFlagLine bool
	}{
		{
			name:         "the process base URL hears the prompt and its refusal is the answer",
			env:          map[string]string{envClaudeBaseURL: "http://sink.test", envClaudeAPIKey: "k1"},
			status:       http.StatusBadRequest,
			wantURLs:     []string{"http://sink.test/v1/messages key=k1"},
			wantStdout:   `API Error: 400 {"type":"error","error":{"message":"captured"}}` + "\n",
			wantFlagLine: true,
		},
		{
			name:         "project settings env overrides the process and local settings override the project",
			env:          map[string]string{envClaudeBaseURL: "http://sink.test", envClaudeAPIKey: "k1"},
			project:      `{"env":{"ANTHROPIC_BASE_URL":"http://gateway.test","ANTHROPIC_API_KEY":"k2"}}`,
			local:        `{"env":{"ANTHROPIC_BASE_URL":"http://local.test/"}}`,
			status:       http.StatusUnauthorized,
			wantURLs:     []string{"http://local.test/v1/messages key=k2"},
			wantStdout:   `API Error: 401 {"type":"error","error":{"message":"captured"}}` + "\n",
			wantFlagLine: true,
		},
		{
			name: "remote flags off leave the experiment line out",
			env: map[string]string{
				envClaudeBaseURL: "http://sink.test", envClaudeAPIKey: "k1", envClaudeNoRemoteFlags: "1",
			},
			status:     http.StatusBadRequest,
			wantURLs:   []string{"http://sink.test/v1/messages key=k1"},
			wantStdout: `API Error: 400 {"type":"error","error":{"message":"captured"}}` + "\n",
		},
		{
			name:         "an unreachable endpoint is Claude's connection error",
			env:          map[string]string{envClaudeBaseURL: "http://down.test", envClaudeAPIKey: "k1"},
			wantURLs:     []string{"http://down.test/v1/messages key=k1"},
			wantStdout:   claudeConnectionError + "\n",
			wantFlagLine: true,
		},
		{
			name:       "no base URL sends nothing and plays the scripted turn",
			wantStdout: "scripted\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{{Type: StepTurn, Reply: "scripted"}}})
			for path, content := range map[string]string{"settings.json": tc.project, "settings.local.json": tc.local} {
				if content == "" {
					continue
				}
				if err := os.MkdirAll(filepath.Join(fix.work, ".claude"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(fix.work, ".claude", path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			catch := &apiCatch{status: tc.status}
			previous := claudeAPITransport
			claudeAPITransport = catch
			t.Cleanup(func() { claudeAPITransport = previous })
			env := map[string]string{envClaudeBaseURL: "", envClaudeAPIKey: "", envClaudeNoRemoteFlags: ""}
			for name, value := range tc.env {
				env[name] = value
			}
			var stdout, stderr bytes.Buffer
			code := Main(context.Background(), "claude", []string{"-p", "x", "--model", "opus"},
				strings.NewReader(""), &stdout, &stderr, fix.env(env))
			wantCode := 1
			if tc.wantURLs == nil {
				wantCode = 0
			}
			if code != wantCode || stdout.String() != tc.wantStdout {
				t.Fatalf("exit %d stdout %q stderr %q; want exit %d stdout %q",
					code, stdout.String(), stderr.String(), wantCode, tc.wantStdout)
			}
			if strings.Join(catch.urls, "|") != strings.Join(tc.wantURLs, "|") {
				t.Fatalf("requests went to %q, want %q", catch.urls, tc.wantURLs)
			}
			if len(catch.bodies) == 0 {
				return
			}
			body := catch.bodies[0]
			wantHead := []apiBlock{
				{Type: blockText, Text: "x-anthropic-billing-header: cc_version=2.1.270; cc_entrypoint=sdk-cli;"},
				{Type: blockText, Text: "You are Claude Code, Anthropic's official CLI for Claude."},
			}
			if body.Model != "opus" || len(body.System) != 3 || body.System[0] != wantHead[0] ||
				body.System[1] != wantHead[1] || len(body.Messages) != 1 || body.Messages[0].Content[0].Text != "x" {
				t.Fatalf("request = %+v, want model opus, the two head blocks %+v and the prompt x", body, wantHead)
			}
			got := body.System[2].Text
			if !tc.wantFlagLine {
				if got != claudeHarnessInstructions {
					t.Fatalf("instructions = %q, want %q", got, claudeHarnessInstructions)
				}
				return
			}
			if !strings.HasPrefix(got, instructions) || len(got) != len(instructions)+len("0123abcd.") {
				t.Fatalf("instructions = %q, want %q then an 8-hex bucket", got, instructions)
			}
		})
	}
}

// TestClaudeRemoteFlagsBucketPerConfigDir pins why doctor pins remote flags off: two captures from two
// fresh config dirs differ in their experiment line, and agree once the flags are off.
func TestClaudeRemoteFlagsBucketPerConfigDir(t *testing.T) {
	for _, noFlags := range []string{"", "1"} {
		fix := newFixture(t)
		t.Chdir(fix.work)
		fix.write(Scenario{SessionID: fixtureSession})
		catch := &apiCatch{status: http.StatusBadRequest}
		previous := claudeAPITransport
		claudeAPITransport = catch
		t.Cleanup(func() { claudeAPITransport = previous })
		for _, dir := range []string{"a", "b"} {
			env := fix.env(map[string]string{
				envClaudeBaseURL: "http://sink.test", envClaudeAPIKey: "k", envClaudeNoRemoteFlags: noFlags,
				"CLAUDE_CONFIG_DIR": filepath.Join(fix.root, dir),
			})
			Main(
				context.Background(),
				"claude",
				[]string{"-p", "x"},
				strings.NewReader(""),
				io.Discard,
				io.Discard,
				env,
			)
		}
		if len(catch.bodies) != 2 {
			t.Fatalf("flags %q: %d requests, want 2", noFlags, len(catch.bodies))
		}
		first, second := catch.bodies[0].System[2].Text, catch.bodies[1].System[2].Text
		if same := first == second; same != (noFlags == "1") {
			t.Fatalf("flags off=%q: captures equal=%v (%q vs %q), want equal only with the flags off",
				noFlags, same, first, second)
		}
	}
}
