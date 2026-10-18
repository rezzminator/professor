package restart

import (
	"bytes"
	"context"
	"errors"
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

func TestExecuteListsEveryEndedChatOnly(t *testing.T) {
	var order []string
	ended := recordedUnit(&order, FamilyChats, "claude a", false, nil, nil)
	ended.Chat = &EndedChat{Engine: "claude", Name: "a", SessionID: "sess-a"}
	survivor := recordedUnit(&order, FamilyChats, "codex b", false, errors.New("socket still answers"), nil)
	survivor.Chat = &EndedChat{Engine: "codex", Name: "b", SessionID: "sess-b"}
	var out bytes.Buffer
	(Plan{Units: []Unit{ended, survivor}}).Execute(context.Background(), &out)
	text := out.String()
	if !strings.Contains(text, "restart: ended 1 chat(s)") || !strings.Contains(text, "ended  claude   a") ||
		!strings.Contains(text, "sess-a") {
		t.Fatalf("the ended chat is not listed with engine, name and session id:\n%s", text)
	}
	if strings.Contains(text, "ended  codex") {
		t.Fatalf("a chat that failed to end is listed as ended:\n%s", text)
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
			{Family: FamilySearch, Err: errors.New("docker ps: exit 1")},
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
		"find   search-backend FAILED  docker ps: exit 1",
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
	if !strings.Contains(out.String(), "nothing found to start again") {
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
	if !strings.Contains(printed, "service  restart (was up: ActiveState=active)") {
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
