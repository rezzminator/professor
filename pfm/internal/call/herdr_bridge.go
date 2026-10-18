package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// HerdrBridge is a pfm tmux chat client proved in a Herdr pane's foreground.
type HerdrBridge struct {
	TmuxSocket, Session string
	ClientPID           int
}

type bridgeProver struct {
	values     paths.Values
	proc       gather.ProcFS
	foreground func(ctx context.Context, socket, pane string) ([]int, error)
	tmux       func(ctx context.Context, socketPath string, args ...string) (string, error)
}

// ProveHerdrBridge proves the Herdr pane hosts a pfm tmux chat client: a
// foreground tmux client of a server under pfm's tmux directory, attached to a
// session holding pfm's Herdr record, whose start identity held across the proof.
func ProveHerdrBridge(ctx context.Context, socket, pane string) (HerdrBridge, error) {
	values, err := paths.Resolve()
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("herdr pane %s bridge ownership unproved: resolve paths: %w", pane, err)
	}
	return bridgeProver{
		values: values,
		proc:   gather.NewProcFS(values.ProcRoot),
		foreground: bridgeForeground(
			herdr.Call,
			func() uint64 { return uint64(clock.Real.Now().UnixNano()) },
		),
		tmux: bridgeTmux,
	}.prove(ctx, socket, pane)
}

// bridgeForeground reads one pane's foreground pids through pane.process_info,
// refusing a reply that does not name that very pane.
func bridgeForeground(
	call func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error),
	seq func() uint64,
) func(ctx context.Context, socket, pane string) ([]int, error) {
	return func(ctx context.Context, socket, pane string) ([]int, error) {
		line, err := herdr.ProcessInfo{Pane: pane, Seq: seq()}.Line()
		if err != nil {
			return nil, fmt.Errorf("render pane.process_info of %s: %w", pane, err)
		}
		results, err := call(ctx, socket, line)
		if err != nil {
			return nil, fmt.Errorf("pane.process_info of %s: %w", pane, err)
		}
		if len(results) != 1 {
			return nil, fmt.Errorf("pane.process_info of %s answered %d results, want 1", pane, len(results))
		}
		var reply struct {
			Info struct {
				Pane string `json:"pane_id"`
			} `json:"process_info"`
		}
		if err := json.Unmarshal(results[0], &reply); err != nil {
			return nil, fmt.Errorf("decode pane.process_info of %s: %w", pane, err)
		}
		if reply.Info.Pane != pane {
			return nil, fmt.Errorf("pane.process_info of %s answered for pane %q", pane, reply.Info.Pane)
		}
		return herdr.ParseProcessInfo(results[0])
	}
}

// bridgeTmux runs one tmux read within gather.ProbeTimeout, as chat's runTmux
// does: a stopped or wedged pfm server is this proof's named failure, never a
// painter stalled on it.
func bridgeTmux(ctx context.Context, socketPath string, args ...string) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, gather.ProbeTimeout)
	defer cancel()
	var stderr strings.Builder
	command := pfmtmux.Exec(readCtx, "", socketPath, args...)
	command.Stderr = &stderr
	// A client killed for a stopped server leaves that server holding its
	// pipe: past the delay Wait closes it and returns.
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil && ctx.Err() == nil && errors.Is(readCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("tmux %s on %s: %w within %s",
			strings.Join(args, " "), socketPath, gather.ErrServerUnresponsive, gather.ProbeTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("tmux %s on %s: %w: %s", strings.Join(args, " "), socketPath, err,
			strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

// prove returns the first foreground pid proved a pfm bridge client; every
// refusal is joined into the error, so an unproved pane names each reason.
func (prover bridgeProver) prove(ctx context.Context, socket, pane string) (HerdrBridge, error) {
	pids, err := prover.foreground(ctx, socket, pane)
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("herdr pane %s bridge ownership unproved: %w", pane, err)
	}
	if len(pids) == 0 {
		return HerdrBridge{}, fmt.Errorf(
			"herdr pane %s bridge ownership unproved: no pfm tmux client in its foreground",
			pane,
		)
	}
	var refusals []error
	for _, pid := range pids {
		bridge, err := prover.client(ctx, pid)
		if err == nil {
			return bridge, nil
		}
		refusals = append(refusals, fmt.Errorf(
			"herdr pane %s bridge client %d ownership unproved: %w",
			pane,
			pid,
			err,
		))
	}
	return HerdrBridge{}, errors.Join(refusals...)
}

// client proves one foreground pid is a pfm tmux client attached to a session
// that carries pfm's Herdr record.
func (prover bridgeProver) client(ctx context.Context, pid int) (HerdrBridge, error) {
	first, err := prover.proc.Stat(pid)
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("read start identity: %w", err)
	}
	argv, err := prover.proc.Cmdline(pid)
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("read command line: %w", err)
	}
	socketPath, err := prover.serverSocket(argv)
	if err != nil {
		return HerdrBridge{}, err
	}
	output, err := prover.tmux(
		ctx,
		socketPath,
		"list-clients",
		"-F",
		"#{client_pid}"+pfmtmux.Separator+"#{session_name}",
	)
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("list clients of %s: %w", socketPath, err)
	}
	session := ""
	for line := range strings.SplitSeq(output, "\n") {
		fields := pfmtmux.FormatSplit(strings.TrimRight(line, "\r"), 2)
		if len(fields) == 2 && fields[0] == strconv.Itoa(pid) {
			session = fields[1]
			break
		}
	}
	if session == "" {
		return HerdrBridge{}, fmt.Errorf("not attached to tmux server %s", socketPath)
	}
	// "=name:" is tmux's exact-session target: a bare "=name" reads as no
	// session at all, and a bare "name" may match another session by prefix.
	// No -q: a failed read carries tmux's reason, never an empty record; tmux
	// refuses an unset user option as "invalid option: @name", the one
	// refusal that means the session holds no record.
	raw, err := prover.tmux(ctx, socketPath, "show-options", "-v", "-t", "="+session+":", herdr.StoredOption)
	if err != nil && strings.Contains(err.Error(), "invalid option: "+herdr.StoredOption) {
		raw, err = "", nil
	}
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("read %s of session %s: %w", herdr.StoredOption, session, err)
	}
	if strings.TrimSpace(raw) == "" {
		return HerdrBridge{}, fmt.Errorf(
			"session %s on %s holds no pfm Herdr record: re-attach the chat in its Herdr tab to record it",
			session,
			socketPath,
		)
	}
	var stored herdr.Stored
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &stored); err != nil {
		return HerdrBridge{}, fmt.Errorf("decode %s of session %s: %w", herdr.StoredOption, session, err)
	}
	// A record whose only content is an empty client map is as empty as {}.
	if len(stored.Clients) == 0 {
		stored.Clients = nil
	}
	if reflect.ValueOf(stored).IsZero() {
		return HerdrBridge{}, fmt.Errorf("session %s on %s holds an empty pfm Herdr record %q", session, socketPath,
			strings.TrimSpace(raw))
	}
	second, err := prover.proc.Stat(pid)
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("reread start identity: %w", err)
	}
	if second.StartTime != first.StartTime {
		return HerdrBridge{}, fmt.Errorf(
			"start identity changed during the proof (%d then %d)",
			first.StartTime,
			second.StartTime,
		)
	}
	return HerdrBridge{TmuxSocket: socketPath, Session: session, ClientPID: pid}, nil
}

// serverSocket reads the tmux server a client command line names: -S wins over
// -L as in tmux, and only a server inside pfm's tmux directory is a pfm server.
func (prover bridgeProver) serverSocket(argv []string) (string, error) {
	if len(argv) == 0 || filepath.Base(argv[0]) != "tmux" {
		return "", fmt.Errorf("not a tmux client: %q", argv)
	}
	var label, socketPath string
	for index := 1; index < len(argv); index++ {
		argument := argv[index]
		if argument == "--" || !strings.HasPrefix(argument, "-") || argument == "-" {
			break
		}
		flag, value := argument[:2], argument[2:]
		switch flag {
		case "-L", "-S", "-f", "-c", "-T":
			if value == "" && index+1 < len(argv) {
				index++
				value = argv[index]
			}
		default:
			continue
		}
		switch flag {
		case "-L":
			label = value
		case "-S":
			socketPath = value
		}
	}
	if socketPath != "" {
		clean := filepath.Clean(socketPath)
		if filepath.Dir(clean) != filepath.Clean(prover.values.TmuxDir) {
			return "", fmt.Errorf("tmux server %s outside pfm's tmux directory %s", socketPath, prover.values.TmuxDir)
		}
		return clean, nil
	}
	if label == "" {
		return "", fmt.Errorf("tmux client names no pfm tmux server: %q", argv)
	}
	resolved, err := prover.values.SocketUnder(label)
	if err != nil {
		return "", fmt.Errorf("tmux client names no pfm tmux server: %w", err)
	}
	return resolved, nil
}
