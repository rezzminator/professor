package restart

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedUnit is a unit whose stop and start write to order; a non-nil
// stopErr or startErr is that step's failure.
func recordedUnit(order *[]string, family, name string, starts bool, stopErr, startErr error) Unit {
	unit := Unit{
		Family: family, Name: name, FoundBy: "test " + name, StopHow: "stop " + name,
		Stop: func(context.Context) (string, error) {
			*order = append(*order, "stop "+name)
			return "stopped", stopErr
		},
	}
	if starts {
		unit.StartHow = "start " + name
		unit.Start = func(context.Context) (string, error) {
			*order = append(*order, "start "+name)
			return "verified up", startErr
		}
	}
	return unit
}

func TestExecuteStopsInOrderThenStartsUpstreamFirst(t *testing.T) {
	var order []string
	plan := Plan{Units: []Unit{
		recordedUnit(&order, FamilyChats, "chat", false, nil, nil),
		recordedUnit(&order, FamilyHerdr, "herdr", true, nil, nil),
		recordedUnit(&order, FamilyServices, "service", true, nil, nil),
	}, Last: "the worker"}
	var out bytes.Buffer
	if code := plan.Execute(context.Background(), &out); code != 0 {
		t.Fatalf("Execute = %d, want 0; output:\n%s", code, out.String())
	}
	want := "stop chat,stop herdr,stop service,start service,start herdr"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	for _, line := range []string{"restart: every step verified", ExitMarker + "0", "last   pfm            the worker"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out.String())
		}
	}
}

func TestExecuteNamesAFailedStepCarriesOnAndExitsNonZero(t *testing.T) {
	var order []string
	plan := Plan{Units: []Unit{
		recordedUnit(&order, FamilyHelpers, "sink", false, errors.New("pid 7 still runs\nafter SIGKILL"), nil),
		recordedUnit(&order, FamilyHerdr, "herdr", true, nil, errors.New("not running after start")),
		recordedUnit(&order, FamilyServices, "service", true, nil, nil),
	}}
	var out bytes.Buffer
	if code := plan.Execute(context.Background(), &out); code != 1 {
		t.Fatalf("Execute = %d, want 1", code)
	}
	if got := strings.Join(order, ","); got != "stop sink,stop herdr,stop service,start service,start herdr" {
		t.Fatalf("a failed step cut the run short: %s", got)
	}
	text := out.String()
	for _, line := range []string{
		"stop   chat-helpers   sink  FAILED  pid 7 still runs after SIGKILL",
		"start  herdr          herdr  FAILED  not running after start",
		"start  services       service  ok  verified up",
		"restart: 2 step(s) FAILED",
		ExitMarker + "1",
	} {
		if !strings.Contains(text, line) {
			t.Fatalf("output lacks %q:\n%s", line, text)
		}
	}
}

func TestExecuteFailsOnAFailedFindingWithoutHidingIt(t *testing.T) {
	plan := Plan{Findings: []Finding{{Family: FamilyHelpers, Err: errors.New("list processes: permission denied")}}}
	var out bytes.Buffer
	if code := plan.Execute(context.Background(), &out); code != 1 {
		t.Fatalf("Execute = %d, want 1: a find that could not look is a failure, never nothing to stop", code)
	}
	if !strings.Contains(out.String(), "find   chat-helpers   FAILED  list processes: permission denied") {
		t.Fatalf("the failed find is not named:\n%s", out.String())
	}
}

// TestExecuteListsEveryEndedChatOnly: the closing report lists a chat as
// ended only once its end was verified, with why it does not come back when
// the restart brings chats back; a chat brought back is listed back once its
// verification passed, and one that failed it is lost, with its reopen line.
func TestExecuteListsEveryEndedChatOnly(t *testing.T) {
	for _, row := range []struct {
		name    string
		restore bool
		want    string
	}{
		{name: "no restore", want: `stop   chats          claude a  ok  stopped
stop   chats          codex b  FAILED  socket still answers
restart: ended 1 chat(s)
  ended  claude   a                        sess-a
last   pfm            
restart: 1 step(s) FAILED
restart: exit 1
`},
		{name: "restore", restore: true, want: `stop   chats          claude a  ok  stopped
stop   chats          codex b  FAILED  socket still answers
stop   chats          opencode c  ok  stopped
stop   chats          claude d  ok  stopped
start  chats          claude d  FAILED  not live again
start  chats          opencode c  ok  verified up
restart: ended 1 chat(s)
  ended  claude   a                        sess-a  not brought back: it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore
restart: brought back 1 chat(s)
  back   opencode c                        sess-c
restart: 1 chat(s) did not come back; reopen each by hand
  lost   claude   d                        sess-d  pfm chat open sess-d
last   pfm            
restart: 2 step(s) FAILED
restart: exit 1
`},
	} {
		t.Run(row.name, func(t *testing.T) {
			var order []string
			ended := recordedUnit(&order, FamilyChats, "claude a", false, nil, nil)
			ended.Chat = &ChatReport{Engine: "claude", Name: "a", SessionID: "sess-a"}
			survivor := recordedUnit(&order, FamilyChats, "codex b", false, errors.New("socket still answers"), nil)
			survivor.Chat = &ChatReport{Engine: "codex", Name: "b", SessionID: "sess-b"}
			units := []Unit{ended, survivor}
			if row.restore {
				ended.Chat.NotBack = "it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore"
				back := recordedUnit(&order, FamilyChats, "opencode c", true, nil, nil)
				back.Chat = &ChatReport{Engine: "opencode", Name: "c", SessionID: "sess-c"}
				lost := recordedUnit(&order, FamilyChats, "claude d", true, nil, errors.New("not live again"))
				lost.Chat = &ChatReport{Engine: "claude", Name: "d", SessionID: "sess-d"}
				units = append(units, back, lost)
			}
			var out bytes.Buffer
			(Plan{Units: units, Restore: row.restore}).Execute(context.Background(), &out)
			if text := out.String(); text != row.want {
				t.Fatalf("closing report:\n%s\nwant:\n%s", text, row.want)
			}
		})
	}
}

func TestPrintShowsThePlanAndRunsNoStep(t *testing.T) {
	never := func(context.Context) (string, error) {
		t.Fatal("Print ran a step")
		return "", nil
	}
	plan := Plan{
		Findings: []Finding{
			{Family: FamilyHerdr, Detail: "server running"},
			{Family: FamilyServices, Err: errors.New("docker ps: exit 1")},
		},
		Units: []Unit{
			{Family: FamilyChats, Name: "claude a", FoundBy: "fleet scan", StopHow: "kill-server", Stop: never},
			{
				Family:   FamilyHerdr,
				Name:     "herdr server",
				FoundBy:  "herdr status",
				StopHow:  "server stop",
				StartHow: "herdr server",
				Stop:     never,
				Start:    never,
			},
		},
		Last: "the worker",
	}
	var out bytes.Buffer
	plan.Print(&out)
	text := out.String()
	for _, line := range []string{
		"find   herdr          ok      server running",
		"find   services       FAILED  docker ps: exit 1",
		"stop   1  chats          claude a", "found: fleet scan", "stop:  kill-server",
		"start  1  herdr          herdr server", "start: herdr server",
		"last      pfm            the worker",
	} {
		if !strings.Contains(text, line) {
			t.Fatalf("plan lacks %q:\n%s", line, text)
		}
	}
	out.Reset()
	(Plan{}).Print(&out)
	if out.String() != `restart plan: stop downstream first, then start upstream first only what was running before; chats are not brought back
start     nothing found to start again
last      pfm            
` {
		t.Fatalf("an empty start phase is not named:\n%s", out.String())
	}
}

func TestStepLineKeepsAnErrorOnOneLine(t *testing.T) {
	if got := stepLine("a\n  b\tc "); got != "a b c" {
		t.Fatalf("stepLine = %q", got)
	}
}

// printAndRun prints a plan of units, then executes it: the dry-run text, the
// result lines and the exit code.
func printAndRun(t *testing.T, units []Unit) (printed, executed string, code int) {
	t.Helper()
	plan := Plan{Units: units, Last: "the worker"}
	var plain, run bytes.Buffer
	plan.Print(&plain)
	code = plan.Execute(context.Background(), &run)
	return plain.String(), run.String(), code
}

// assertStaysStopped proves the plan and the result lines both name unit as
// staying stopped, with the state it was found in.
func assertStaysStopped(t *testing.T, printed, executed, unit, state string) {
	t.Helper()
	want := unit + "  stays stopped (was not running before: " + state + ")"
	if !strings.Contains(printed, want) {
		t.Fatalf("the plan does not name %q:\n%s", want, printed)
	}
	if !strings.Contains(executed, want) {
		t.Fatalf("the result lines do not name %q:\n%s", want, executed)
	}
}

func TestPrintMarksEachStartAsARestartOfAUnitThatWasUp(t *testing.T) {
	var order []string
	unit := recordedUnit(&order, FamilyServices, "service", true, nil, nil)
	unit.Before = "ActiveState=active"
	printed, _, _ := printAndRun(t, []Unit{unit})
	if printed != `restart plan: stop downstream first, then start upstream first only what was running before; chats are not brought back
stop   1  services       service
          found: test service
          stop:  stop service
start  1  services       service  restart (was up: ActiveState=active)
          start: start service
last      pfm            the worker
` {
		t.Fatalf("the start line does not say the unit was up:\n%s", printed)
	}
}

func TestExecuteNeverStopsOrStartsAUnitThatWasDown(t *testing.T) {
	never := func(context.Context) (string, error) {
		t.Fatal("a unit that was down before was stopped or started")
		return "", nil
	}
	down := Unit{
		Family: FamilyServices, Name: "systemd pfm-x.service", FoundBy: "test", Before: "ActiveState=inactive",
		StaysStopped: true, Stop: never, Start: never,
	}
	printed, executed, code := printAndRun(t, []Unit{down})
	if code != 0 {
		t.Fatalf("Execute = %d, want 0:\n%s", code, executed)
	}
	assertStaysStopped(t, printed, executed, "systemd pfm-x.service", "ActiveState=inactive")
	if strings.Contains(printed, "stop   1") || strings.Contains(printed, "start  1") {
		t.Fatalf("the plan numbers a unit that stays stopped as a step:\n%s", printed)
	}
}

// golden compares got with testdata/{file}; a mismatch prints the GOLDEN line the fill script writes from.
func golden(t *testing.T, file, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil || string(want) != got {
		t.Errorf(
			"testdata/%s: %v\nwant:\n%s\nactual:\n%s\nGOLDEN internal/restart/testdata/%s = %q",
			file,
			err,
			want,
			got,
			file,
			got,
		)
	}
}
