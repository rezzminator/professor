package run

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func opencodeMachine(binary, home string) pfmconfig.Config {
	return pfmconfig.Config{
		OpenCode:         pfmconfig.OpenCodePrefs{Binary: binary},
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: home}},
	}
}

func opencodeHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "opencode")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestArgumentsOpenCodeMapsModelEffortFormatAndNativeArgs(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		request Request
		want    []string
	}{
		{
			name:    "normalized gpt model",
			request: Request{Engine: pfmengine.OpenCode, Model: "gpt-5.6-luna", Effort: "high", Args: []string{"--future", "value"}},
			want:    []string{"run", "--model", "openai/gpt-5.6-luna", "--variant", "high", "--format", "json", "--future", "value"},
		},
		{
			name:    "provider-qualified native model",
			request: Request{Engine: pfmengine.OpenCode, Model: "openai/gpt-5.6-luna", Native: true},
			want:    []string{"run", "--model", "openai/gpt-5.6-luna"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := arguments(testCase.request)
			if err != nil {
				t.Fatalf("arguments() error = %v", err)
			}
			if !equalStrings(got, testCase.want) {
				t.Fatalf("arguments() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestValidateOpenCodeArgsRejectsOnlyConflictingControls(t *testing.T) {
	base := Request{Engine: pfmengine.OpenCode, Model: "model", Effort: "medium"}
	cases := []struct {
		name    string
		request Request
		arg     string
		wantErr bool
	}{
		{name: "model", request: base, arg: "--model=other", wantErr: true},
		{name: "variant", request: base, arg: "--variant", wantErr: true},
		{name: "pure", request: base, arg: "--pure", wantErr: true},
		{name: "format normalized", request: base, arg: "--format", wantErr: true},
		{
			name:    "format native",
			request: Request{Engine: pfmengine.OpenCode, Native: true},
			arg:     "--format",
			wantErr: false,
		},
		{name: "dir without cwd", request: base, arg: "--dir", wantErr: false},
		{
			name:    "dir with cwd",
			request: Request{Engine: pfmengine.OpenCode, CWD: t.TempDir()},
			arg:     "--dir",
			wantErr: true,
		},
		{name: "agent without replacement", request: base, arg: "--agent", wantErr: false},
		{
			name:    "agent with replacement",
			request: Request{Engine: pfmengine.OpenCode, SystemPrompt: stringPtr("replacement")},
			arg:     "--agent",
			wantErr: true,
		},
		{
			name:    "session without persistence",
			request: Request{Engine: pfmengine.OpenCode, NoSessionPersistence: true},
			arg:     "--session",
			wantErr: true,
		},
		{name: "session with persistence", request: base, arg: "--session", wantErr: false},
		{
			name:    "sealed native arg",
			request: Request{Engine: pfmengine.OpenCode, Sealed: true},
			arg:     "--future-flag",
			wantErr: true,
		},
		{name: "future flag", request: base, arg: "--future-flag", wantErr: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateOpenCodeArgs([]string{testCase.arg}, testCase.request)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validateOpenCodeArgs(%q) error = %v, wantErr=%v", testCase.arg, err, testCase.wantErr)
			}
		})
	}
}

func TestValidateOpenCodeArgsNamesRoutedCodexSelector(t *testing.T) {
	for _, testCase := range []struct {
		selector string
		want     string
	}{
		{selector: "codex", want: `--engine codex runs through OpenCode; OpenCode native argument "-c" conflicts with an explicit pfm control`},
		{selector: "cx", want: `--engine cx runs through OpenCode; OpenCode native argument "-c" conflicts with an explicit pfm control`},
		{selector: "opencode", want: `OpenCode native argument "-c" conflicts with an explicit pfm control`},
	} {
		t.Run(testCase.selector, func(t *testing.T) {
			request := Request{Config: selectorConfig(), NoSessionPersistence: true}
			if err := ApplyEngineSelector(&request, testCase.selector); err != nil {
				t.Fatal(err)
			}
			err := validateOpenCodeArgs([]string{"-c"}, request)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("validateOpenCodeArgs(-c) error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestResolveOpenCodeUsesConfiguredBinaryAccountAndPrefs(t *testing.T) {
	headlessJail(t)
	binary := engineBinary(t)
	accountHome := opencodeHome(t)
	config := opencodeMachine(binary, accountHome)
	config.Ask.Prefs = map[pfmengine.ID]pfmconfig.EnginePrefs{
		pfmengine.OpenCode: {Model: "gpt-5.6-luna", Effort: "high"},
	}
	resolved, err := Resolve(Request{
		Config: config, Engine: pfmengine.OpenCode, ConfigDir: accountHome,
		Native: true,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Account != 7 || resolved.ConfigDir != accountHome {
		t.Fatalf("resolved account/config dir = %d/%q, want 7/%q", resolved.Account, resolved.ConfigDir, accountHome)
	}
	if resolved.Model != "gpt-5.6-luna" || resolved.Effort != "high" {
		t.Fatalf("native OpenCode prefs = %q/%q, want gpt-5.6-luna/high", resolved.Model, resolved.Effort)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestOpenCodeReadinessOfflineRecordsWarn(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := server.URL
	server.Close()
	for _, tc := range []struct {
		name string
		call func(context.Context) (bool, error)
	}{
		{"health", func(ctx context.Context) (bool, error) { return openCodeServerHealthy(ctx, address, nil) }},
		{"catalog", func(ctx context.Context) (bool, error) { return openCodeToolCatalog(ctx, address, "/tmp", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			ready, err := tc.call(ctx)
			if ready || err != nil {
				t.Fatalf("ready=%v err=%v", ready, err)
			}
			records := recorder.Records()
			if len(records) != 1 || records[0].Level != "WARN" {
				t.Fatalf("records=%s", recorder.Raw())
			}
		})
	}
}

func TestOpenCodeRefusalReasons(t *testing.T) {
	for _, row := range []struct {
		name, body, want string
		status           int
	}{
		{"plain", "  seeding unpinned\n", "seeding unpinned", 501},
		{"nested JSON", `{"name":"BadRequest","data":{"message":"model not found"}}`, "model not found", 400},
		{"message", `{"message":"m"}`, "m", 400},
		{"error", `{"error":"e"}`, "e", 400},
		{"JSON fallback", `{"name":"BadRequest"}`, `{"name":"BadRequest"}`, 400},
		{"multiline", "line one\n\nline two", "line one line two", 500},
		{"long Unicode", strings.Repeat("é", 2000), strings.Repeat("é", 256) + "…", 502},
		{"empty", "", "(empty response body)", 403},
		{"whitespace", " \n\t ", "(empty response body)", 403},
	} {
		t.Run(row.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(row.status)
				_, _ = w.Write([]byte(row.body))
			}))
			defer server.Close()
			_, err := openCodeJSON(context.Background(), http.MethodPost, server.URL+"/session", "/tmp", nil, nil)
			want := fmt.Sprintf("OpenCode API POST /session failed with HTTP status %d: %s", row.status, row.want)
			if err == nil || err.Error() != want {
				t.Fatalf("err=%v want=%q", err, want)
			}
		})
	}
}

func TestOpenCodeRefusalReadiness(t *testing.T) {
	for _, row := range []struct {
		name, path, body, want string
		status                 int
		call                   func(context.Context, string) (bool, error)
	}{
		{"health", "/global/health", "bad credentials", "OpenCode health request failed with HTTP status 401: bad credentials", 401, func(ctx context.Context, address string) (bool, error) {
			return openCodeServerHealthy(ctx, address, nil)
		}},
		{"health retry", "/global/health", "down", "", 500, func(ctx context.Context, address string) (bool, error) {
			return openCodeServerHealthy(ctx, address, nil)
		}},
		{"catalog forbidden", "/experimental/tool/ids", "denied", "OpenCode tool catalog request failed with HTTP status 403: denied", 403, func(ctx context.Context, address string) (bool, error) {
			return openCodeToolCatalog(ctx, address, "/tmp", nil)
		}},
		{"catalog server", "/experimental/tool/ids", "boom", "OpenCode tool catalog request failed with HTTP status 500 after server became healthy: boom", 500, func(ctx context.Context, address string) (bool, error) {
			return openCodeToolCatalog(ctx, address, "/tmp", nil)
		}},
		{"catalog missing", "/experimental/tool/ids", "missing", "OpenCode tool catalog endpoint is unavailable", 404, func(ctx context.Context, address string) (bool, error) {
			return openCodeToolCatalog(ctx, address, "/tmp", nil)
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != row.path {
					t.Errorf("path=%q", r.URL.Path)
				}
				w.WriteHeader(row.status)
				_, _ = w.Write([]byte(row.body))
			}))
			defer server.Close()
			ready, err := row.call(context.Background(), server.URL)
			if ready || (row.want == "" && err != nil) || (row.want != "" && (err == nil || err.Error() != row.want)) {
				t.Fatalf("ready=%v err=%v want=%q", ready, err, row.want)
			}
		})
	}
}

func TestOpenCodeJSONSuccessBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"seed"}`))
		}
	}))
	defer server.Close()
	for _, row := range []struct{ method, want string }{{http.MethodPost, `{"id":"seed"}`}, {http.MethodDelete, ""}} {
		body, err := openCodeJSON(context.Background(), row.method, server.URL+"/session", "/tmp", nil, nil)
		if err != nil || string(body) != row.want {
			t.Fatalf("method=%s body=%q err=%v", row.method, body, err)
		}
	}
}
