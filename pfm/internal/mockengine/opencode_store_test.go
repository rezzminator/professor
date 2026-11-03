package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
)

func TestOpenCodeTUIWritesTheStorePfmReads(t *testing.T) {
	for _, launch := range []bool{false, true} {
		t.Run(fmt.Sprint(launch), func(t *testing.T) {
			fix := newFixture(t)
			fix.write(
				Scenario{
					Pane: openCodeShapes,
					Steps: []Step{
						{Type: StepTurn, Reply: "first reply", BusyMS: 600, Tokens: &Tokens{Input: 120, Output: 30}},
					},
				},
			)
			args := []string{fix.work, "--session", fixtureOpenCodeSession, "--model", "fixture/model"}
			if launch {
				args = append(args, "--prompt", "prove the bound")
			}
			session := fix.startTUI("opencode", args, nil)
			session.waitFrame(
				"composer",
				func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) },
			)
			if !launch {
				session.typeLine("prove the bound")
			}
			session.waitFrame(
				"pfm busy footer",
				func(frame string) bool { return pfmcall.IsBusyFor(pfmengine.OpenCode, frame) },
			)
			session.waitFrame("idle reply", func(frame string) bool {
				return strings.Contains(frame, "⏺ first reply") && !pfmcall.IsBusyFor(pfmengine.OpenCode, frame)
			})
			root := filepath.Join(fix.home, ".local", "share", "opencode")
			rows, err := index.ReadOpenCodeSessions(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ID != fixtureOpenCodeSession || rows[0].Directory != fix.work ||
				rows[0].ProjectDir != fix.work ||
				rows[0].FirstPrompt != "prove the bound" ||
				rows[0].PromptCount != 1 ||
				rows[0].AssistantCount != 1 ||
				rows[0].TokensInput != 120 ||
				rows[0].TokensOutput != 30 ||
				rows[0].Model != "fixture/model" {
				t.Fatalf("pfm OpenCode sessions=%+v", rows)
			}
			if !gather.IsOpenCodeCommand(append([]string{"opencode"}, args...)) {
				t.Fatal("pfm detector refuses TUI argv")
			}
			if title := gather.OpenCodePaneName(
				"OC | " + rows[0].Title,
			); title != "prove the bound" ||
				!strings.Contains(session.out.all(), "\x1b]0;OC | "+title+"\a") {
				t.Fatalf("pane title does not bind session %q: %q", title, session.out.all())
			}
			live, err := gather.DetectOpenCode(gather.RealProcFS{Root: fix.procRoot}, []gather.ProbePane{{
				Socket: "ox-1700000000-4242-7", PaneID: "%9", PID: os.Getpid(),
				SessionName: "fixture", CurrentPath: fix.work, PaneTitle: "OC | " + rows[0].Title,
			}}, []gather.OpenCodeSession{{
				ID: rows[0].ID, Title: rows[0].Title,
				Directory: rows[0].Directory, TimeCreatedMS: rows[0].TimeCreatedMS,
			}})
			if err != nil || len(live) != 1 || live[0].SessionID != fixtureOpenCodeSession {
				t.Fatalf("pfm detector seats=%+v err=%v", live, err)
			}
			session.typeLine(`again mock-engine: {"type":"turn","reply":"second reply"}`)
			session.waitFrame(
				"second reply",
				func(frame string) bool { return strings.Contains(frame, "⏺ second reply") },
			)
			rows, err = index.ReadOpenCodeSessions(context.Background(), root)
			if err != nil || len(rows) != 1 || rows[0].PromptCount != 2 || rows[0].AssistantCount != 2 ||
				rows[0].FirstPrompt != "prove the bound" {
				t.Fatalf("continued TUI sessions=%+v err=%v", rows, err)
			}
		})
	}
}

func TestOpenCodeNativeTitleUpdatesStoreAndOSCWithoutTitleHook(t *testing.T) {
	fix, prototype, receipts, _ := openCodePluginFixture(t)
	fix.write(Scenario{SessionID: fixtureOpenCodeSession, Pane: openCodeShapes, Steps: []Step{
		{Type: "native_title", Name: "native title"}, {Type: StepTurn, Reply: "retitled"},
	}})
	session := fix.startTUI(
		engineOpenCode,
		[]string{fix.work},
		map[string]string{"OPENCODE_CONFIG_CONTENT": prototype.proc.env("OPENCODE_CONFIG_CONTENT"), "HERDR_ENV": "1"},
	)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine("original title")
	session.waitFrame("native title reply", func(frame string) bool { return strings.Contains(frame, "⏺ retitled") })
	root := filepath.Join(fix.home, ".local", "share", "opencode")
	rows, err := index.ReadOpenCodeSessions(context.Background(), root)
	if err != nil || len(rows) != 1 || rows[0].Title != "native title" {
		t.Fatalf("native title rows=%+v err=%v", rows, err)
	}
	if !strings.Contains(session.out.all(), "\x1b]0;OC | native title\a") {
		t.Fatalf("native OSC missing: %q", session.out.all())
	}
	session.typeLine("/rename user title")
	waitFile(t, receipts, func(content string) bool { return strings.Contains(content, `"title":"user title"`) })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	var titles []string
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, receipts)), "\n") {
		var event struct {
			Type       string `json:"type"`
			Properties struct {
				Info struct {
					Title string `json:"title"`
				} `json:"info"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "session.updated" {
			titles = append(titles, event.Properties.Info.Title)
		}
	}
	if want := []string{"original title", "user title"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("%s: titles=%q want=%q", receipts, titles, want)
	}
}
