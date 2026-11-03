package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

const startupTimeout = 30 * time.Second

func (proc *process) startupEvent(event string) error {
	raw, err := json.Marshal(struct {
		PID   int    `json:"pid"`
		Event string `json:"event"`
	}{proc.pid, event})
	if err != nil {
		return err
	}
	return proc.recorder.write("startup.jsonl", string(raw)+"\n")
}

type processBirth struct {
	PID       int    `json:"pid"`
	Start     uint64 `json:"start"`
	ParentPID int    `json:"parent_pid"`
	Group     int    `json:"pgid"`
	CWD       string `json:"cwd"`
}

func (scenario Scenario) validateStartup(fields map[string]json.RawMessage) error {
	for _, path := range []struct{ name, value string }{
		{"ready_hold", scenario.ReadyHold}, {"app_server_stdout_hold", scenario.AppServerStdoutHold},
	} {
		if path.value != "" && !filepath.IsAbs(path.value) {
			return fmt.Errorf("%s must be an absolute path", path.name)
		}
	}
	if _, present := fields["ready_timeout_ms"]; present && scenario.ReadyTimeoutMS <= 0 {
		return errors.New("ready_timeout_ms must be positive")
	}
	if scenario.StartupExitCode != nil && (*scenario.StartupExitCode < 0 || *scenario.StartupExitCode > 255) {
		return errors.New("startup_exit_code must be in 0..255")
	}
	return nil
}

func (scenario Scenario) startupBound() time.Duration {
	if scenario.ReadyTimeoutMS > 0 {
		return time.Duration(scenario.ReadyTimeoutMS) * time.Millisecond
	}
	return startupTimeout
}

func (proc *process) birth(name string) error {
	if proc.recorder.dir == "" {
		return nil
	}
	table, err := gather.OpenProcFS("")
	if err != nil {
		return fmt.Errorf("read mock birth: %w", err)
	}
	stat, err := table.Stat(proc.pid)
	if err != nil {
		return fmt.Errorf("read mock birth pid %d: %w", proc.pid, err)
	}
	group, err := syscall.Getpgid(proc.pid)
	if err != nil {
		return fmt.Errorf("read mock process group pid %d: %w", proc.pid, err)
	}
	raw, err := json.Marshal(processBirth{proc.pid, stat.StartTime, stat.ParentPID, group, proc.cwd})
	if err != nil {
		return fmt.Errorf("encode mock birth: %w", err)
	}
	return proc.recorder.write(name, string(raw)+"\n")
}

func (proc *process) startup() error {
	if err := proc.birth("birth.json"); err != nil {
		return err
	}
	if proc.script.ReadyHold == "" {
		return nil
	}
	return waitStartupPath(proc.ctx, proc.script.ReadyHold, proc.script.startupBound())
}

func waitStartupPath(ctx context.Context, path string, bound time.Duration) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("startup gate must be an absolute path: %s", path)
	}
	bounded, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read startup gate %s: %w", path, err)
		}
		select {
		case <-bounded.Done():
			return fmt.Errorf("wait startup gate %s: %w", path, bounded.Err())
		case <-ticker.C:
		}
	}
}
