// Package agentopen owns the takeover/attach seam for Claude daemon agents.
// It is intentionally internal: the picker is its only product caller.
package agentopen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

var ErrBusy = errors.New("another agent open is already in flight")

type OutsidePFMError struct {
	Name, Parent string
	PID          int
}

func (failure *OutsidePFMError) Error() string {
	return fmt.Sprintf(
		"⚙ %s: running outside pfm (pid %d, parent %s) — no pane to attach; kill %d and open the row to resume it in a pane",
		failure.Name,
		failure.PID,
		failure.Parent,
		failure.PID,
	)
}

const (
	agentQueryTimeout = 40 * time.Second
	unknownState      = "unknown"
)

type Agent struct {
	SessionID string `json:"sessionId"`
	ShortID   string `json:"id"`
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	State     string `json:"state"`
}

// Claude has emitted pid as both a JSON number and a quoted number across
// agent-registry versions; accept either without turning a valid registry
// into an apparent absence.
func (agent *Agent) UnmarshalJSON(content []byte) error {
	var wire struct {
		SessionID string          `json:"sessionId"`
		ShortID   string          `json:"id"`
		Name      string          `json:"name"`
		PID       json.RawMessage `json:"pid"`
		Status    string          `json:"status"`
		State     string          `json:"state"`
	}
	if err := json.Unmarshal(content, &wire); err != nil {
		return err
	}
	pid := 0
	if len(wire.PID) != 0 && string(wire.PID) != "null" {
		value := strings.Trim(string(wire.PID), "\"")
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse agent pid %q: %w", value, err)
		}
		pid = parsed
	}
	*agent = Agent{
		SessionID: wire.SessionID,
		ShortID:   wire.ShortID,
		Name:      wire.Name,
		PID:       pid,
		Status:    wire.Status,
		State:     wire.State,
	}
	return nil
}

func (agent Agent) activity() string {
	if agent.Status != "" {
		return agent.Status
	}
	if agent.State != "" {
		return agent.State
	}
	return unknownState
}

// ParseAgents validates the complete JSON response. A malformed response is
// an unanswered registry, never an empty registry.
func ParseAgents(content []byte) ([]Agent, error) {
	var agents []Agent
	if err := json.Unmarshal(content, &agents); err != nil {
		return nil, fmt.Errorf("parse claude agents response: %w", err)
	}
	return agents, nil
}

func findAgent(agents []Agent, id string) (Agent, bool) {
	for _, agent := range agents {
		if agent.SessionID == id || (agent.ShortID != "" && strings.HasPrefix(id, agent.ShortID)) {
			return agent, true
		}
	}
	return Agent{}, false
}

type Request struct {
	ID             string
	CWD            string
	OwningConfig   string
	PrimaryAccount int
	Cache1H        bool
}

type Account struct {
	ID        int
	ConfigDir string
}

type Process struct {
	PID  int
	Argv []string
}

type Commands interface {
	QueryAgents(context.Context, string) ([]byte, error)
	Resume(context.Context, string, string, string, bool) error
	View(context.Context, string, string) error
}

type Processes interface {
	Processes(context.Context) ([]Process, error)
	Alive(int) bool
	Terminate(int) error
	Kill(int) error
}

type Tmux interface {
	SocketForPID(context.Context, int) (string, error)
	Attach(context.Context, string) error
}

type Dependencies struct {
	SIDDir       string
	Home         string
	Accounts     []Account
	ClaudeBinary string
	Commands     Commands
	Processes    Processes
	Tmux         Tmux
	Stderr       io.Writer
	GracePeriod  time.Duration
	PollInterval time.Duration
}

type Opener struct {
	sidDir, home              string
	accounts                  []Account
	claudeBinary              string
	commands                  Commands
	processes                 Processes
	tmux                      Tmux
	stderr                    io.Writer
	gracePeriod, pollInterval time.Duration
	configurationError        error
}

func New(dependencies Dependencies) *Opener {
	grace := dependencies.GracePeriod
	if grace <= 0 {
		grace = 15 * time.Second
	}
	poll := dependencies.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	accounts := append([]Account(nil), dependencies.Accounts...)
	var configurationError error
	if len(accounts) == 0 {
		configurationError = errors.New("agent opener configured account roster is empty")
	}
	claudeBinary := dependencies.ClaudeBinary
	if claudeBinary == "" {
		claudeBinary = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	return &Opener{
		sidDir: dependencies.SIDDir, home: dependencies.Home,
		accounts:     accounts,
		claudeBinary: claudeBinary,
		commands:     dependencies.Commands, processes: dependencies.Processes,
		tmux: dependencies.Tmux, stderr: stderr,
		gracePeriod: grace, pollInterval: poll,
		configurationError: configurationError,
	}
}

func (opener *Opener) Open(ctx context.Context, request Request) error {
	if opener == nil {
		return errors.New("agent opener is nil")
	}
	if opener.configurationError != nil {
		return opener.configurationError
	}
	if request.ID == "" || strings.ContainsAny(request.ID, "/\\\x00") {
		return errors.New("agent open requires a safe session id")
	}
	if opener.commands == nil || opener.processes == nil || opener.tmux == nil {
		return errors.New("agent opener dependencies are incomplete")
	}
	if opener.sidDir == "" {
		return errors.New("agent opener sid directory is empty")
	}
	if err := os.MkdirAll(opener.sidDir, 0o700); err != nil {
		return fmt.Errorf("create agent takeover lock directory: %w", err)
	}
	lock, err := os.OpenFile(
		filepath.Join(opener.sidDir, ".takeover-"+request.ID+".lock"),
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open agent takeover lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			fmt.Fprintf(opener.stderr, "pfm internal agent-open: close takeover lock %s: %v\n", request.ID, err)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			fmt.Fprintf(
				opener.stderr,
				"pfm internal agent-open: another open/takeover of %s is already in flight — let it settle, then retry (or attach its window).\n",
				request.ID,
			)
			return ErrBusy
		}
		return fmt.Errorf("lock agent takeover: %w", err)
	}
	defer func() {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			fmt.Fprintf(opener.stderr, "pfm internal agent-open: unlock takeover %s: %v\n", request.ID, err)
		}
	}()

	configs := opener.configCandidates(request.OwningConfig)
	hit, hitConfig, found, registryComplete := opener.lookup(ctx, request.ID, configs)
	if !found {
		if !registryComplete {
			return fmt.Errorf(
				"no agent registry answered completely for %s — refusing to treat an error as absence",
				request.ID,
			)
		}
		held, err := opener.holdsSession(ctx, request.ID)
		if err != nil {
			return fmt.Errorf("prove no live claude holds %s: %w", request.ID, err)
		}
		if held {
			return fmt.Errorf(
				"no registry row for %s, but a live claude holds it — refusing the double-resume",
				request.ID,
			)
		}
		primary := opener.configForAccount(request.PrimaryAccount)
		fmt.Fprintf(
			opener.stderr,
			"no live agent found for %s — resuming fresh (account %d)\n",
			request.ID,
			effectiveAgentAccount(request.PrimaryAccount),
		)
		return opener.resumeFresh(ctx, request, primary, primary)
	}

	account := opener.accountForConfig(hitConfig)
	if hit.PID > 0 && opener.processes.Alive(hit.PID) {
		socket, err := opener.tmux.SocketForPID(ctx, hit.PID)
		if err != nil {
			return fmt.Errorf("locate tmux socket for agent %d: %w", hit.PID, err)
		}
		if id, ok := pfmengine.FromSocket(socket); ok && id == pfmengine.Claude {
			fmt.Fprintf(
				opener.stderr,
				"⚙ %s is a tmux-resident chat on %s — attaching its window\n",
				agentDisplayName(hit),
				socket,
			)
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
				return fmt.Errorf("release attach lock: %w", err)
			}
			return opener.tmux.Attach(ctx, socket)
		}
		parent := unknownState
		if processes, ok := opener.processes.(interface{ ParentComm(int) string }); ok {
			if comm := strings.TrimSpace(processes.ParentComm(hit.PID)); comm != "" {
				parent = comm
			}
		}
		return &OutsidePFMError{Name: agentDisplayName(hit), PID: hit.PID, Parent: parent}
	}

	if hit.activity() == "busy" {
		fmt.Fprintf(
			opener.stderr,
			"⚙ %s (account %d) is BUSY — attaching. Pick '%s' in the view; ⌃C detaches.\n",
			agentDisplayName(hit),
			account,
			agentDisplayName(hit),
		)
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			return fmt.Errorf("release attach lock: %w", err)
		}
		return opener.commands.View(ctx, hitConfig, request.CWD)
	}

	primary := opener.configForAccount(request.PrimaryAccount)
	fmt.Fprintf(
		opener.stderr,
		"⚙ %s (account %d, state: %s) — taking over → fresh resume under account %d\n",
		agentDisplayName(hit),
		account,
		hit.activity(),
		effectiveAgentAccount(request.PrimaryAccount),
	)
	if hit.PID > 0 && opener.processes.Alive(hit.PID) {
		if err := opener.processes.Terminate(hit.PID); err != nil {
			return fmt.Errorf("stop agent %d: %w", hit.PID, err)
		}
		deadline := time.NewTimer(opener.gracePeriod)
		ticker := time.NewTicker(opener.pollInterval)
		for opener.processes.Alive(hit.PID) {
			select {
			case <-ctx.Done():
				ticker.Stop()
				deadline.Stop()
				return ctx.Err()
			case <-deadline.C:
				ticker.Stop()
				if opener.processes.Alive(hit.PID) {
					if err := opener.processes.Kill(hit.PID); err != nil {
						return fmt.Errorf("kill agent %d: %w", hit.PID, err)
					}
				}
				goto stopped
			case <-ticker.C:
			}
		}
		ticker.Stop()
		deadline.Stop()
	}
stopped:
	return opener.resumeFresh(ctx, request, primary, hitConfig)
}

func (opener *Opener) lookup(ctx context.Context, id string, configs []string) (Agent, string, bool, bool) {
	answered := make(map[string]bool, len(configs))
	for pass := 0; pass < 2; pass++ {
		for _, config := range configs {
			queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
			content, err := opener.commands.QueryAgents(queryCtx, config)
			cancel()
			if err != nil {
				fmt.Fprintf(
					opener.stderr,
					"pfm internal agent-open: query agent registry config %q (pass %d): %v\n",
					config,
					pass+1,
					err,
				)
				continue
			}
			agents, err := ParseAgents(content)
			if err != nil {
				fmt.Fprintf(
					opener.stderr,
					"pfm internal agent-open: parse agent registry config %q (pass %d): %v\n",
					config,
					pass+1,
					err,
				)
				continue
			}
			answered[config] = true
			if agent, ok := findAgent(agents, id); ok {
				return agent, config, true, true
			}
		}
	}
	return Agent{}, "", false, len(answered) == len(configs)
}

func (opener *Opener) resumeFresh(ctx context.Context, request Request, config, fallback string) error {
	err := opener.commands.Resume(ctx, config, request.CWD, request.ID, request.Cache1H)
	if err == nil {
		return nil
	}
	fmt.Fprintf(
		opener.stderr,
		"pfm internal agent-open: resume %s under config %q failed: %v\n",
		request.ID,
		config,
		err,
	)
	fmt.Fprintf(
		opener.stderr,
		"\nresume still refused — falling back to the agent view (pick the session to attach):\n",
	)
	viewErr := opener.commands.View(ctx, fallback, request.CWD)
	if viewErr == nil {
		return nil
	}
	fmt.Fprintf(
		opener.stderr,
		"pfm internal agent-open: fallback agent view under config %q failed: %v\n",
		fallback,
		viewErr,
	)
	return errors.Join(err, viewErr)
}

func (opener *Opener) holdsSession(ctx context.Context, id string) (bool, error) {
	rows, err := opener.processes.Processes(ctx)
	if err != nil {
		fmt.Fprintf(opener.stderr, "pfm internal agent-open: scan live claude holders for %s: %v\n", id, err)
		return false, err
	}
	for _, row := range rows {
		isClaude := len(row.Argv) > 0 &&
			filepath.Base(row.Argv[0]) == filepath.Base(opener.claudeBinary)
		if !isClaude {
			continue
		}
		for _, argument := range row.Argv[1:] {
			if argument == id {
				return true, nil
			}
		}
	}
	return false, nil
}

func (opener *Opener) configCandidates(owning string) []string {
	if owning != "" {
		return []string{owning}
	}
	result := make([]string, 0, len(opener.accounts))
	for _, account := range opener.accounts {
		result = append(result, account.ConfigDir)
	}
	return result
}

func (opener *Opener) configForAccount(account int) string {
	for _, candidate := range opener.accounts {
		if candidate.ID == account {
			return candidate.ConfigDir
		}
	}
	return ""
}

func (opener *Opener) accountForConfig(config string) int {
	for _, account := range opener.accounts {
		if filepath.Clean(config) == filepath.Clean(account.ConfigDir) {
			return account.ID
		}
	}
	return 1
}

func effectiveAgentAccount(account int) int {
	return account
}

func agentDisplayName(agent Agent) string {
	if agent.Name != "" {
		return agent.Name
	}
	return "(unnamed)"
}
