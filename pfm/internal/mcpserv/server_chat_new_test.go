package mcpserv

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestChatNewCacheChoiceAndValidation(t *testing.T) {
	t.Parallel()
	var calls [][]string
	service := newService("test", &backend{dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return 0
	}})
	for _, cache := range []string{"5m", "1h"} {
		if _, _, err := service.chatNew(context.Background(), nil, NewInput{Name: "child", Cache: cache}); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{
		{"chat", "new", "--name", "child", "--cache", "5m"},
		{"chat", "new", "--name", "child", "--cache", "1h"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new calls = %q, want %q", calls, want)
	}
	if _, _, err := service.chatNew(
		context.Background(), nil, NewInput{Name: "child", Cache: "on"},
	); err == nil || !strings.Contains(err.Error(), "1h|5m") {
		t.Fatalf("invalid cache error = %v", err)
	}
	if len(calls) != len(want) {
		t.Fatalf("invalid cache dispatched: %q", calls)
	}
}

func TestChatNewTimeoutForwarding(t *testing.T) {
	t.Parallel()
	zero, seconds := 0, 30
	for _, test := range []struct {
		name    string
		timeout *int
		want    []string
	}{
		{name: "unset", want: []string{"chat", "new", "--name", "child", "--await"}},
		{name: "forever", timeout: &zero, want: []string{"chat", "new", "--name", "child", "--await", "--timeout", "0"}},
		{name: "seconds", timeout: &seconds, want: []string{"chat", "new", "--name", "child", "--await", "--timeout", "30"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			service := newService(
				"test",
				&backend{dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
					calls = append(calls, append([]string(nil), args...))
					return 0
				}},
			)
			_, _, err := service.chatNew(
				context.Background(),
				nil,
				NewInput{Name: "child", Await: true, Timeout: test.timeout},
			)
			if err != nil || !reflect.DeepEqual(calls, [][]string{test.want}) {
				t.Fatalf("chat_new timeout = %v, calls %q; want %q", err, calls, test.want)
			}
		})
	}
}

func TestChatNewCacheSchemaEnum(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{})
	protocol := connectInMemory(t, service.server)
	listed, err := protocol.clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "chat_new" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties["cache"].Enum; !reflect.DeepEqual(got, []string{"1h", "5m"}) {
			t.Fatalf("cache schema enum = %q", got)
		}
		return
	}
	t.Fatal("chat_new tool missing")
}

func TestChatNewCarriesTheAgentRole(t *testing.T) {
	var calls [][]string
	service := newService("test", &backend{dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		return 0
	}})
	input := NewInput{Name: "seat", Model: "claude-sonnet-5-5", Effort: "xhigh", AgentRole: "flights-foreman"}
	if _, _, err := service.chatNew(context.Background(), nil, input); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{
		"chat", "new", "--name", "seat", "--model", "claude-sonnet-5-5", "--effort", "xhigh",
		"--agent-role", "flights-foreman",
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new calls = %q, want %q", calls, want)
	}
}

// TestChatNewDescriptionRoutesEveryModelRunHere reads chat_new as a caller
// does: the description within the 600-rune tool budget, its example call
// naming only real schema fields, the launchers it replaces and the cleanup
// named, and every schema field described.
func TestChatNewDescriptionRoutesEveryModelRunHere(t *testing.T) {
	tool := listedChatNew(t)
	description := tool.Description
	if runes := utf8.RuneCountInString(description); runes > 600 {
		t.Fatalf("chat_new description is %d runes, over 600: %q", runes, description)
	}
	for _, phrase := range []string{"model run", "pfm headless exec", "claude -p", "chat_kill", "sub-agent"} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("chat_new description lacks %q: %q", phrase, description)
		}
	}
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	example := regexp.MustCompile(`chat_new\{([^}]*)\}`).FindStringSubmatch(description)
	if example == nil {
		t.Fatalf("chat_new description has no example call: %q", description)
	}
	for _, key := range regexp.MustCompile(`(\w+):`).FindAllStringSubmatch(example[1], -1) {
		if _, found := schema.Properties[key[1]]; !found {
			t.Fatalf("chat_new example names %q, not a schema field: %q", key[1], example[0])
		}
	}
	for name, property := range schema.Properties {
		if strings.TrimSpace(property.Description) == "" {
			t.Fatalf("chat_new schema field %q has no description", name)
		}
	}
}

func listedChatNew(t *testing.T) *mcp.Tool {
	t.Helper()
	service := newService("test", &backend{})
	protocol := connectInMemory(t, service.server)
	listed, err := protocol.clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "chat_new" {
			return tool
		}
	}
	t.Fatal("chat_new tool missing")
	return nil
}

func chatNewWorkbenchFixture(t *testing.T) (string, string) {
	t.Helper()
	root := testjail.Fleet(t)
	dir := filepath.Join(root, "acme", "docs", "scribe")
	for path, body := range map[string]string{
		filepath.Join(root, "acme", ".professor", "baseline.json"): "{}",
		paths.WorkbenchManifest(dir):                               `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"):              "You are scribe.",
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, dir
}

func TestChatNewWorkbench(t *testing.T) {
	for _, name := range []string{"auto-name", "outside", "no cwd", "named", "caller disabled", "caller enabled", "caller cwd", "roster error"} {
		t.Run(name, func(t *testing.T) {
			root, dir := chatNewWorkbenchFixture(t)
			var calls [][]string
			backend := &backend{
				warnings: io.Discard,
				paths:    paths.Values{TmuxDir: filepath.Join(root, "tmux")},
				dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
					calls = append(calls, append([]string(nil), args...))
					return 0
				},
			}
			service := newService("test", backend)
			input := NewInput{CWD: dir}
			want := []string{"chat", "new", "--name", "_SCRIBE:1", "--cwd", dir}
			var request *mcp.CallToolRequest
			switch name {
			case "outside":
				input.CWD = filepath.Join(root, "acme", "src")
			case "no cwd":
				input.CWD = ""
			case "named":
				input = NewInput{Name: "child"}
				want = []string{"chat", "new", "--name", "child"}
			case "caller disabled", "caller enabled", "caller cwd":
				engine, kind, socket := pfmengine.Codex, compose.LiveCodex, "cx-caller"
				if name != "caller disabled" {
					engine, kind, socket = pfmengine.Claude, compose.LiveClaude, "cc-caller"
				}
				backend.chat = &fakeChatVerbs{
					listed: chat.ListResult{
						Rows: []compose.Row{
							{
								Kind:        kind,
								ID:          "caller-id",
								Name:        "caller",
								SessionName: "caller",
								Socket:      socket,
								PaneID:      "%0",
								CWD:         dir,
							},
						},
						Matched: 1,
					},
				}
				request = &mcp.CallToolRequest{
					Params: &mcp.CallToolParamsRaw{
						Meta: mcp.Meta{
							"pfmProxy": map[string]any{
								"v":       ProxyWireVersion,
								"engine":  string(engine),
								"session": "caller",
							},
						},
					},
				}
				if name != "caller disabled" {
					want = []string{"chat", "new", "--name", "_SCRIBE:1", "--engine", "cc", "--cwd", dir}
				}
				if name == "caller cwd" {
					input.CWD = ""
				}
			case "roster error":
				blocker := filepath.Join(root, "blocker")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
			}
			_, _, err := service.chatNew(context.Background(), request, input)
			if name == "outside" {
				if err == nil || err.Error() != "name is required outside a workbench" || len(calls) != 0 {
					t.Fatalf("outside = %v, calls %q", err, calls)
				}
				return
			}
			if name == "no cwd" {
				if err == nil ||
					err.Error() != "name is required: no working directory is known to look up a workbench" ||
					len(calls) != 0 {
					t.Fatalf("no cwd = %v, calls %q", err, calls)
				}
				return
			}
			if name == "roster error" {
				if err == nil || !strings.Contains(err.Error(), "chat_new: name the chat:") || len(calls) != 0 {
					t.Fatalf("roster = %v, calls %q", err, calls)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(calls, [][]string{want}) {
				t.Fatalf("chat_new = %v, calls %q; want %q", err, calls, want)
			}
		})
	}
}

func TestChatNewWorkbenchReservations(t *testing.T) {
	t.Run("concurrent", func(t *testing.T) {
		root, dir := chatNewWorkbenchFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
		defer release()
		var mutex sync.Mutex
		var calls [][]string
		service := newService("test", &backend{
			warnings: io.Discard,
			paths:    paths.Values{TmuxDir: filepath.Join(root, "tmux")},
			dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
				mutex.Lock()
				calls = append(calls, append([]string(nil), args...))
				first := len(calls) == 1
				mutex.Unlock()
				if first {
					close(firstStarted)
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return 1
					}
				}
				return 0
			},
		})
		firstDone := make(chan error, 1)
		go func() {
			_, _, err := service.chatNew(ctx, nil, NewInput{CWD: dir})
			firstDone <- err
		}()
		select {
		case <-firstStarted:
		case <-ctx.Done():
			t.Fatal("first unnamed chat never reached its launch")
		}
		secondDone := make(chan error, 1)
		go func() {
			_, _, err := service.chatNew(ctx, nil, NewInput{CWD: dir})
			secondDone <- err
		}()
		select {
		case err := <-secondDone:
			if err != nil {
				t.Errorf("second unnamed chat = %v", err)
			}
		case <-ctx.Done():
			t.Fatal("second unnamed chat blocked behind the first launch")
		}
		select {
		case err := <-firstDone:
			t.Fatalf("first launch returned before release: %v", err)
		default:
		}
		mutex.Lock()
		got := append([][]string(nil), calls...)
		mutex.Unlock()
		want := [][]string{
			{"chat", "new", "--name", "_SCRIBE:1", "--cwd", dir},
			{"chat", "new", "--name", "_SCRIBE:2", "--cwd", dir},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("concurrent chat_new calls = %q, want %q", got, want)
		}
		release()
		select {
		case err := <-firstDone:
			if err != nil {
				t.Fatalf("first unnamed chat = %v", err)
			}
		case <-ctx.Done():
			t.Fatal("first unnamed chat did not finish after release")
		}
	})
	t.Run("failed launch releases name", func(t *testing.T) {
		root, dir := chatNewWorkbenchFixture(t)
		var calls [][]string
		service := newService("test", &backend{
			warnings: io.Discard,
			paths:    paths.Values{TmuxDir: filepath.Join(root, "tmux")},
			dispatch: func(_ context.Context, args []string, _, stderr io.Writer) int {
				calls = append(calls, append([]string(nil), args...))
				if len(calls) == 1 {
					_, _ = io.WriteString(stderr, "launch failed")
					return 1
				}
				return 0
			},
		})
		_, output, err := service.chatNew(context.Background(), nil, NewInput{CWD: dir})
		if err == nil || output.Status != statusError || output.Code != 1 || output.Message != "launch failed" {
			t.Fatalf("failed launch = %#v, %v", output, err)
		}
		_, _, err = service.chatNew(context.Background(), nil, NewInput{CWD: dir})
		want := []string{"chat", "new", "--name", "_SCRIBE:1", "--cwd", dir}
		if err != nil || !reflect.DeepEqual(calls, [][]string{want, want}) {
			t.Fatalf("chat_new after failed launch = %v, calls %q; want name reused", err, calls)
		}
	})
}

func TestChatNewWorkbenchNameSchema(t *testing.T) {
	tool := listedChatNew(t)
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range schema.Required {
		if field == "name" {
			t.Fatal("name required inside a workbench")
		}
	}
	if !strings.Contains(schema.Properties["name"].Description, "inside a workbench, empty takes its next {name}:{n}") {
		t.Fatalf("name schema: %s", raw)
	}
}
