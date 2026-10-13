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

// TestOpenWatchesTheLaunch pins what both open doors report about the pane
// they just created: a pane whose process exits at launch is a failed open
// carrying the pane's own words (its server killed, never left for a lister
// to count), a server already gone is a failed open, and a pane met by Claude
// Code's folder-trust dialog is an open that warns with the attach command
// while the dialog stays up untouched.
func TestOpenWatchesTheLaunch(t *testing.T) {
	const socket = "cc-907-1-1"
	trustScreen := strings.Join([]string{
		"Accessing workspace:",
		"",
		" /work/resume",
		"",
		" Quick safety check: Is this a project you created or one you trust?",
		"",
		" ❯ 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm · Esc to cancel",
	}, "\n")
	for _, test := range []struct {
		name        string
		launch      fakeLaunch
		wantErr     []string
		wantWarning []string
		wantKilled  []string
		// cancelled: the caller has stopped waiting before the watch reads
		// the pane, so every context-bound read fails.
		cancelled bool
		// processes is the machine's process table; maxReads, when set, is
		// how many reads of the pane the watch may take before it answers.
		processes []Process
		maxReads  int
		// wantCleared: the pane left alive gets remain-on-exit back off, so a
		// chat that ends later takes its server with it as before.
		wantCleared bool
	}{
		{
			name:        "a chat that settles",
			launch:      fakeLaunch{screen: "Claude Code\n\n> \n", command: "claude"},
			wantCleared: true,
		},
		{
			name: "the pane's process exits at launch",
			launch: fakeLaunch{
				screen: "pfm internal agent-open: refusing the double-resume of " +
					"66666666-6666-4666-8666-666666666666\n\n[exited]\n",
				dead: true,
			},
			wantErr:    []string{"resumable chat exited at launch", "refusing the double-resume"},
			wantKilled: []string{socket},
		},
		{
			name:    "the server is gone before the watch reads it",
			launch:  fakeLaunch{gone: true},
			wantErr: []string{"resumable chat died at launch", socket, "what it printed was lost"},
		},
		{
			// A resume row's pane runs its compound line under a shell that
			// never execs, and an agent row's under pfm: the engine is a
			// child on the pane's terminal, never the pane's own command.
			name:        "an engine running under the pane's shell",
			launch:      fakeLaunch{screen: "Claude Code\n\n> \n", command: "bash", tty: "pts/7"},
			processes:   []Process{{PID: 4242, Argv: []string{"/opt/bin/claude", "--resume"}, TTY: "pts/7"}},
			maxReads:    2,
			wantCleared: true,
		},
		{
			name:   "a live server whose pane cannot be read",
			launch: fakeLaunch{unreadable: true},
			wantWarning: []string{
				"pfm could not read resumable chat's launch pane",
				"tmux -L " + socket + " attach -t " + socket,
			},
			wantCleared: true,
		},
		{
			name:      "the caller stops waiting",
			launch:    fakeLaunch{screen: "Claude Code\n\n> \n", command: "claude"},
			cancelled: true,
			wantWarning: []string{
				"pfm stopped watching resumable chat's launch",
				"tmux -L " + socket + " attach -t " + socket,
			},
			wantCleared: true,
		},
		{
			name:   "Claude Code's folder-trust dialog",
			launch: fakeLaunch{screen: trustScreen, command: "claude"},
			wantWarning: []string{
				"resumable chat is held at Claude Code's folder-trust dialog for /work/resume",
				"pfm pressed nothing",
				"tmux -L " + socket + " attach -t " + socket,
			},
			wantCleared: true,
		},
	} {
		for _, detached := range []bool{true, false} {
			door := "attached"
			if detached {
				door = "detached"
			}
			t.Run(test.name+" ("+door+")", func(t *testing.T) {
				home := t.TempDir()
				stageProfessorPrompt(t, home)
				jailAction(t)
				t.Cleanup(launchnotice.Replace(strings.NewReader(""), false))
				var stderr bytes.Buffer
				tmux := &fakeActionTmux{
					alive:    map[string]bool{},
					launches: map[string]fakeLaunch{socket: test.launch},
				}
				executor, err := New(Dependencies{
					Tmux: tmux, Processes: &fakeProcesses{processes: test.processes},
					Gate: fixedGate(false), Runner: &captureRunner{}, Stderr: &stderr,
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
					PrimaryAccount: 1, Home: home, FreshSocket: socket, Config: machine,
				}
				ctx := context.Background()
				if test.cancelled {
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = cancelled
				}
				var warnings []string
				var line string
				if detached {
					var result OpenResult
					result, err = executor.OpenDetached(ctx, request)
					warnings = result.Warnings
				} else {
					line, err = executor.Open(ctx, request)
					if stderr.Len() > 0 {
						warnings = []string{stderr.String()}
					}
				}
				if len(test.wantErr) > 0 {
					if err == nil {
						t.Fatalf("open reported ok for a launch that died; line %q warnings %q", line, warnings)
					}
					for _, want := range test.wantErr {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("error %q lacks %q", err, want)
						}
					}
					if line != "" {
						t.Fatalf("a failed open still returned the eval line %q", line)
					}
				} else if err != nil {
					t.Fatalf("open failed: %v", err)
				}
				joined := strings.Join(warnings, "\n")
				for _, want := range test.wantWarning {
					if !strings.Contains(joined, want) {
						t.Fatalf("warnings %q lack %q", warnings, want)
					}
				}
				if len(test.wantWarning) == 0 && len(test.wantErr) == 0 && joined != "" {
					t.Fatalf("unexpected warnings %q", warnings)
				}
				if !reflect.DeepEqual(tmux.killedServer, test.wantKilled) {
					t.Fatalf("killed servers = %q, want %q", tmux.killedServer, test.wantKilled)
				}
				if test.maxReads > 0 && tmux.launchReads > test.maxReads {
					t.Fatalf("the watch read the pane %d times, want it up by read %d", tmux.launchReads, test.maxReads)
				}
				if test.wantCleared {
					want := []string{socket + "\ton", socket + "\toff"}
					if !reflect.DeepEqual(tmux.remain, want) {
						t.Fatalf("remain-on-exit = %q, want %q", tmux.remain, want)
					}
				}
			})
		}
	}
}
