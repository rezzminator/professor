package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Claude Code sends a running chat to a background job: the conversation goes on under a new session id in a
// daemon process (`claude bg-spare`, its argv naming no session, its session only in its per-process record
// {config}/sessions/{pid}.json), and the old transcript gets a continued-in record naming the successor. The
// host pane keeps the id it launched with: its statusline goes on naming the old segment.
const (
	claudeBGSpare        = "bg-spare"
	claudeContinuedIn    = "continued-in"
	envBGStage           = "MOCK_ENGINE_BG_STAGE"
	envBGSession         = "MOCK_ENGINE_BG_SESSION"
	envBGHost            = "MOCK_ENGINE_BG_HOST"
	envBGConfig          = "MOCK_ENGINE_BG_CONFIG"
	bgStageLaunch        = "launch"
	bgStageServe         = "serve"
	backgroundJobPolling = 250 * time.Millisecond
)

// claudeJob is the host pane's own identity after its chat moved to a background job: what its statusline names.
type claudeJob struct {
	hostSession, hostTranscript string
}

// startBackgroundJob starts the daemon process serving successor; the unit tests replace it.
var startBackgroundJob = spawnBackgroundJob

// continueInBackground plays StepBackgroundJob: the successor transcript seeded from the running one (its title
// carried), the continued-in record appended to the old one, the daemon started, and the session moved on.
// The job's own hooks run in the daemon, which this fake does not play: the host fires none for it.
func (session *claudeSession) continueInBackground() error {
	if session.job != nil {
		return errors.New("the chat already runs in a background job")
	}
	successor := uuid.NewV4().String()
	path := filepath.Join(filepath.Dir(session.transcript), successor+".jsonl")
	if err := seedClaudeFork(session.transcript, path, successor); err != nil {
		return err
	}
	record, err := json.Marshal(struct {
		Type        string `json:"type"`
		ContinuedIn string `json:"continuedInSessionId"`
		SessionID   string `json:"sessionId"`
		Timestamp   string `json:"timestamp"`
	}{claudeContinuedIn, successor, session.sessionID, stamp(time.Now())})
	if err != nil {
		return fmt.Errorf("encode continued-in record: %w", err)
	}
	if err := appendLine(session.transcript, record); err != nil {
		return err
	}
	if err := startBackgroundJob(session.proc, session.configDir, successor); err != nil {
		return err
	}
	session.job = &claudeJob{hostSession: session.sessionID, hostTranscript: session.transcript}
	session.sessionID, session.transcript = successor, path
	if session.title == "" {
		return nil
	}
	return session.write(claudeRecord{Type: claudeCustomTitle, CustomTitle: session.title, SessionID: successor})
}

// spawnBackgroundJob runs `claude bg-spare` at its launch stage, which starts the serving daemon in a session
// of its own and exits: the daemon is reparented away from the pane, with no tmux in its environment.
func spawnBackgroundJob(proc *process, configDir, successor string) error {
	command, err := bgSpareCommand(proc.ctx, append(withoutTmux(os.Environ()),
		envBGStage+"="+bgStageLaunch, envBGSession+"="+successor,
		envBGHost+"="+strconv.Itoa(os.Getpid()), envBGConfig+"="+configDir))
	if err != nil {
		return err
	}
	command.Dir = proc.cwd
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("start claude %s: %w: %s", claudeBGSpare, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// bgSpareCommand is this binary run as `claude bg-spare`, under the argv0 it was started with.
func bgSpareCommand(ctx context.Context, environ []string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate the mock engine for claude %s: %w", claudeBGSpare, err)
	}
	command := exec.CommandContext(ctx, executable, claudeBGSpare)
	command.Args[0], command.Env = os.Args[0], environ
	return command, nil
}

func withoutTmux(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "TMUX=") && !strings.HasPrefix(entry, "TMUX_PANE=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// serveBGSpare is `claude bg-spare`: the launch stage starts the serve stage detached and exits; the serve
// stage keeps the job's process record until its host pane's process is gone or it is stopped.
func serveBGSpare(proc *process) int {
	switch proc.env(envBGStage) {
	case bgStageLaunch:
		environ := append(os.Environ(), envBGStage+"="+bgStageServe)
		command, err := bgSpareCommand(context.Background(), environ)
		if err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Start(); err != nil {
			warn(proc.stderr, "start claude %s: %v", claudeBGSpare, err)
			return ExitUsage
		}
		if err := command.Process.Release(); err != nil {
			warn(proc.stderr, "release claude %s: %v", claudeBGSpare, err)
		}
		return 0
	case bgStageServe:
		host, err := strconv.Atoi(proc.env(envBGHost))
		if err != nil {
			warn(proc.stderr, "claude %s host pid %q: %v", claudeBGSpare, proc.env(envBGHost), err)
			return ExitUsage
		}
		if err := serveBackgroundJob(proc.ctx, proc.env(envBGConfig), proc.env(envBGSession), host); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
		return 0
	}
	warn(proc.stderr, "claude %s needs %s=%s|%s", claudeBGSpare, envBGStage, bgStageLaunch, bgStageServe)
	return ExitUsage
}

// serveBackgroundJob writes this process's record of the job's session (pid, sessionId, kind "bg", procStart
// as pfm reads a process start) and holds it until ctx ends or host exits, then removes it.
func serveBackgroundJob(ctx context.Context, configDir, session string, host int) error {
	pid := os.Getpid()
	stat, err := gather.NewProcFS(paths.SystemProcRoot()).Stat(pid)
	if err != nil {
		return fmt.Errorf("claude %s start time: %w", claudeBGSpare, err)
	}
	record, err := json.Marshal(struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Kind      string `json:"kind"`
		ProcStart string `json:"procStart"`
	}{pid, session, "bg", strconv.FormatUint(stat.StartTime, 10)})
	if err != nil {
		return fmt.Errorf("encode claude %s record: %w", claudeBGSpare, err)
	}
	path := filepath.Join(configDir, "sessions", strconv.Itoa(pid)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(record, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	ticker := time.NewTicker(backgroundJobPolling)
	defer ticker.Stop()
	var probeErr error
	for ctx.Err() == nil {
		alive, err := gather.ProcessAlive(host)
		if err != nil {
			probeErr = fmt.Errorf("claude %s probe of host %d: %w", claudeBGSpare, host, err)
		}
		if !alive || err != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	if err := os.Remove(path); err != nil {
		return errors.Join(probeErr, fmt.Errorf("remove %s: %w", path, err))
	}
	return probeErr
}

// backgroundJob plays a background_job step; an engine without background jobs refuses it by name.
func (loop *paneLoop) backgroundJob() (int, bool) {
	engine, plays := loop.engine.(interface{ continueInBackground() error })
	if !plays {
		warn(loop.proc.stderr, "%s plays no %s step", loop.proc.engine, StepBackgroundJob)
		return ExitUnpinned, true
	}
	if err := engine.continueInBackground(); err != nil {
		warn(loop.proc.stderr, "background job: %v", err)
		return ExitUnpinned, true
	}
	return 0, false
}
