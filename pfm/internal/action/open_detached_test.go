package action

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestOpenDetachedResumableSpawnsThroughTheSpawnDoor(t *testing.T) {
	home := t.TempDir()
	jailAction(t)
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "66666666-6666-4666-8666-666666666666"
	request := Request{
		Row: compose.Row{
			Kind: compose.ResumeClaude,
			ID:   id,
			CWD:  "/work/resume",
			Name: "resumable chat",
		},
		PrimaryAccount: 1,
		Home:           home,
		FreshSocket:    "cc-905-1-1",
		Config:         testMachineConfig(home),
	}
	result, err := executor.OpenDetached(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmux.created) != 1 {
		t.Fatalf("spawn door called %d times, want 1: %#v", len(tmux.created), tmux.created)
	}
	if tmux.created[0].Socket != "cc-905-1-1" {
		t.Fatalf("spawned server socket = %q, want cc-905-1-1", tmux.created[0].Socket)
	}
	if !reflect.DeepEqual(result, OpenResult{Name: "resumable chat", Socket: "cc-905-1-1", State: "opened"}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestOpenDetachedLiveSpawnsNothing(t *testing.T) {
	home := t.TempDir()
	jailAction(t)
	tmux := &fakeActionTmux{alive: map[string]bool{"cc-100-1-1": true}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Row: compose.Row{
			Kind:        compose.LiveClaude,
			ID:          "77777777-7777-4777-8777-777777777777",
			Socket:      "cc-100-1-1",
			SessionName: "live-session",
			Name:        "live chat",
		},
		PrimaryAccount: 1,
		Home:           home,
		FreshSocket:    "cc-900-1-1",
		Config:         testMachineConfig(home),
	}
	result, err := executor.OpenDetached(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmux.created) != 0 {
		t.Fatalf("spawn door called for an already-live chat: %#v", tmux.created)
	}
	if !reflect.DeepEqual(result, OpenResult{Name: "live chat", Socket: "cc-100-1-1", State: "live"}) {
		t.Fatalf("result = %#v", result)
	}
}

// TestOpenDetachedRecordsItsTrail: the detached door is a coordinator like
// Executor.Open, and it owes the same state trail — requested → opened →
// done. Without it a chat opened over MCP leaves no trace in the activity
// log, and the only chats with a recorded birth are the ones opened from a
// terminal.
func TestOpenDetachedRecordsItsTrail(t *testing.T) {
	home := t.TempDir()
	jailAction(t)
	ctx, recorder := obs.Test(t)
	executor, err := New(Dependencies{
		Tmux:      &fakeActionTmux{alive: map[string]bool{}},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.OpenDetached(ctx, Request{
		Row: compose.Row{
			Kind: compose.ResumeClaude,
			ID:   "55555555-5555-4555-8555-555555555555",
			CWD:  "/work/resume",
			Name: "trailed chat",
		},
		PrimaryAccount: 1,
		Home:           home,
		FreshSocket:    "cc-906-1-1",
		Config:         testMachineConfig(home),
	}); err != nil {
		t.Fatal(err)
	}
	var opened bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "action" {
			continue
		}
		if next, _ := record.Field("next"); next == "opened" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("OpenDetached() reached no opened state: %s", recorder.Raw())
	}
}

func TestOpenDetachedNilExecutor(t *testing.T) {
	var executor *Executor
	if _, err := executor.OpenDetached(context.Background(), Request{}); err == nil {
		t.Fatal("want an error for a nil executor, got nil")
	}
}

func TestOpenDetachedCodexRequiresLogin(t *testing.T) {
	jailAction(t)
	home := t.TempDir()
	machine := testMachineConfig(home)
	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.OpenDetached(context.Background(), Request{
		Row:    compose.Row{Kind: compose.ResumeCodex, ID: "t1", CWD: "/work"},
		Config: machine, Home: home, PrimaryAccount: 1, FreshSocket: "cx-fresh",
	})
	want := "open detached: Codex account 1: " + authPath + " "
	if !errors.Is(err, pfmconfig.ErrCodexLoggedOut) || !strings.HasPrefix(err.Error(), want) || len(tmux.created) != 0 {
		t.Fatalf("OpenDetached error=%v created=%d; want %q", err, len(tmux.created), want)
	}
}

// Opening a chat, attached or detached, tells the person opening it when the
// fleet prompt its policy asks for cannot reach the chat, and opens it anyway.
func TestOpenWarnsWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	for _, door := range []string{"open", "open detached", "open refused"} {
		for _, staged := range []bool{false, true} {
			if door == "open refused" && staged {
				continue // a staged prompt has nothing to warn about, refused or not
			}
			t.Run(door+"/staged="+strconv.FormatBool(staged), func(t *testing.T) {
				home := t.TempDir()
				if staged {
					stageProfessorPrompt(t, home)
				}
				jailAction(t)
				tmux := &fakeActionTmux{alive: map[string]bool{}}
				if door == "open refused" {
					tmux.createErr = errors.New("server refused")
				}
				var stderr bytes.Buffer
				executor, err := New(Dependencies{
					Tmux: tmux, Processes: &fakeProcesses{}, Gate: fixedGate(false),
					Runner: &captureRunner{}, Stderr: &stderr,
				})
				if err != nil {
					t.Fatal(err)
				}
				machine := testMachineConfig(home)
				machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
				request := Request{
					Row: compose.Row{
						Kind: compose.ResumeClaude, ID: "66666666-6666-4666-8666-666666666666",
						CWD: "/work/resume", Name: "resumable chat",
					},
					PrimaryAccount: 1, Home: home, FreshSocket: "cc-905-1-1", Config: machine,
				}
				// The attached open prints on its stderr; the detached one has no
				// terminal, so its result carries the warnings back.
				var told []string
				if door == "open refused" {
					// A chat that never started is never told it started without
					// its prompt: the refusal is the whole story.
					if _, err = executor.Open(context.Background(), request); err == nil {
						t.Fatal("Open succeeded on a refused server")
					}
					if strings.Contains(stderr.String(), "WITHOUT") {
						t.Fatalf("refused open told %q, want no prompt warning", stderr.String())
					}
					return
				}
				if door == "open" {
					_, err = executor.Open(context.Background(), request)
					told = strings.Split(strings.TrimSpace(stderr.String()), "\n")
				} else {
					var result OpenResult
					result, err = executor.OpenDetached(context.Background(), request)
					told = result.Warnings
					if stderr.Len() != 0 {
						t.Fatalf(
							"detached open wrote %q to stderr, want its result to carry the warnings",
							stderr.String(),
						)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(tmux.created) != 1 {
					t.Fatalf("the chat was not opened: created %#v", tmux.created)
				}
				all := strings.Join(told, "\n")
				if staged {
					if strings.Contains(all, "WITHOUT") {
						t.Fatalf("told %q, want no prompt warning", told)
					}
					return
				}
				if !strings.Contains(all, "Claude started WITHOUT its system prompt file") ||
					!strings.Contains(all, "no source repository recorded") {
					t.Fatalf("told %q, want the prompt warning naming the missing clone", told)
				}
			})
		}
	}
}

// A terminal that is about to be attached to would cover the warning, so the
// attached open holds for Enter once; the detached open attaches nothing and
// never waits, and a launch with nothing to warn about never asks.
func TestOpenHoldsForEnterOnlyWhenAWarningIsAboutToBeCovered(t *testing.T) {
	for _, test := range []struct {
		name     string
		detached bool
		staged   bool
		terminal bool
		wantWait bool
	}{
		{"attached, warned, on a terminal", false, false, true, true},
		{"attached, warned, off a terminal", false, false, false, false},
		{"attached, nothing to warn about", false, true, true, false},
		{"detached never waits", true, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.staged {
				stageProfessorPrompt(t, home)
			}
			jailAction(t)
			input := strings.NewReader("\n")
			t.Cleanup(launchnotice.Replace(input, test.terminal))
			var stderr bytes.Buffer
			executor, err := New(Dependencies{
				Tmux: &fakeActionTmux{alive: map[string]bool{}}, Processes: &fakeProcesses{},
				Gate: fixedGate(false), Runner: &captureRunner{}, Stderr: &stderr,
			})
			if err != nil {
				t.Fatal(err)
			}
			machine := testMachineConfig(home)
			machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
			request := Request{
				Row: compose.Row{
					Kind: compose.ResumeClaude, ID: "77777777-7777-4777-8777-777777777777",
					CWD: "/work/resume", Name: "resumable chat",
				},
				PrimaryAccount: 1, Home: home, FreshSocket: "cc-906-1-1", Config: machine,
			}
			if test.detached {
				_, err = executor.OpenDetached(context.Background(), request)
			} else {
				_, err = executor.Open(context.Background(), request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if waited := input.Len() == 0; waited != test.wantWait {
				t.Fatalf("read Enter = %t, want %t; stderr %q", waited, test.wantWait, stderr.String())
			}
			if asked := strings.Contains(stderr.String(), launchnotice.EnterPrompt); asked != test.wantWait {
				t.Fatalf("asked for Enter = %t, want %t; stderr %q", asked, test.wantWait, stderr.String())
			}
		})
	}
}
