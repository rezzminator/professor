package reload

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestEngineLiveUsesThePaneProcessPIDNotTheTmuxPaneID(t *testing.T) {
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"claude"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	live, err := engineLive(proc, 700, pfmengine.Claude, "", "")
	if err != nil || !live {
		t.Fatalf("engineLive() = %v, %v", live, err)
	}
}

func TestEngineLiveIgnoresAProcessThatExitsDuringTheProcScan(t *testing.T) {
	proc := fakeReloadProc{
		pids:   []int{800, 801},
		argv:   map[int][]string{801: {"claude"}},
		cmdErr: map[int]error{800: os.ErrNotExist},
		stat:   map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	live, err := engineLive(proc, 700, pfmengine.Claude, "", "")
	if err != nil || !live {
		t.Fatalf("engineLive() = %v, %v", live, err)
	}
}

func TestEngineLiveSkipsAProcessWhoseCommandCannotBeRead(t *testing.T) {
	proc := fakeReloadProc{
		pids:   []int{1, 801},
		argv:   map[int][]string{801: {"claude"}},
		cmdErr: map[int]error{1: fmt.Errorf("read kern.procargs2 for 1: %w", syscall.EINVAL)},
		stat:   map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	live, err := engineLive(proc, 700, pfmengine.Claude, "", "")
	if err != nil || !live {
		t.Fatalf("engineLive() = %v, %v; want true, nil", live, err)
	}
}

func TestEngineLiveNamesTheUnreadableProcessesWhenNothingMatches(t *testing.T) {
	proc := fakeReloadProc{
		pids:   []int{42, 801},
		argv:   map[int][]string{801: {"zsh"}},
		cmdErr: map[int]error{42: fmt.Errorf("read kern.procargs2 for 42: %w", syscall.EINVAL)},
		stat:   map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	live, err := engineLive(proc, 700, pfmengine.Claude, "", "")
	if live || err == nil {
		t.Fatalf("engineLive() = %v, %v; want false and an error naming the skipped processes", live, err)
	}
	for _, want := range []string{"unreadable and skipped", "first pid 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("engineLive() error = %q, want it to contain %q", err, want)
		}
	}
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("engineLive() error = %v, want it to wrap syscall.EINVAL", err)
	}
}
