package action

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

type Purpose = claudelaunch.Purpose

const (
	PurposeInteractive = claudelaunch.PurposeInteractive
	PurposeResume      = claudelaunch.PurposeResume
	PurposeLauncher    = claudelaunch.PurposeLauncher
	PurposeQuery       = claudelaunch.PurposeQuery
)

// ClaudeSpawn prints or executes the launch returned by the registry.
type ClaudeSpawn struct {
	Purpose    Purpose
	Account    int
	Cache1H    *bool
	SessionID  string
	Resume     string
	Fork       bool
	Name       string
	Model      string
	Effort     string
	PromptFile string
	Args       []string
	Home       string
	Machine    pfmconfig.Config
	Runner     deps.Runner

	binary            string
	explicitConfigDir string
}

func (spawn ClaudeSpawn) request() claudelaunch.Request {
	return claudelaunch.Request{
		Purpose: spawn.Purpose, Account: spawn.Account, Cache1H: spawn.Cache1H,
		SessionID: spawn.SessionID, Resume: spawn.Resume, Fork: spawn.Fork,
		Name: spawn.Name, Model: spawn.Model, Effort: spawn.Effort,
		PromptFile: spawn.PromptFile, Args: spawn.Args, Home: spawn.Home,
		Binary: spawn.binary, ConfigDir: spawn.explicitConfigDir,
	}
}

func (spawn ClaudeSpawn) render() (claudelaunch.Launch, error) {
	if spawn.Purpose < PurposeInteractive || spawn.Purpose > PurposeQuery {
		return claudelaunch.Launch{}, fmt.Errorf("claude spawn: unknown purpose %d", spawn.Purpose)
	}
	// spawn.Name is no NUL candidate: Render strips control runes from it
	// (naming.LaunchName), so a label read from a transcript never fails a resume.
	values := []string{
		spawn.Home, spawn.Model, spawn.Effort, spawn.binary,
		spawn.explicitConfigDir, spawn.SessionID, spawn.Resume, spawn.PromptFile,
	}
	values = append(values, spawn.Args...)
	if hasNUL(values...) {
		return claudelaunch.Launch{}, errors.New("claude spawn values cannot contain NUL")
	}
	return claudelaunch.Render(spawn.request(), spawn.Machine)
}

func (spawn ClaudeSpawn) ShellCommand() (string, error) {
	launch, err := spawn.render()
	if err != nil {
		return "", err
	}
	var command strings.Builder
	command.WriteString(envStripWords(launch.Unset))
	writeAssignments(&command, launch.Env)
	command.WriteByte(' ')
	command.WriteString(Quote(launch.Binary))
	for _, argument := range launch.Argv {
		command.WriteByte(' ')
		command.WriteString(Quote(argument))
	}
	return command.String(), nil
}

// ProcessCommand keeps the direct callers' stdio and directory controls.
type ProcessCommand struct {
	ctx    context.Context
	runner deps.Runner
	Path   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (spawn ClaudeSpawn) Command(ctx context.Context) (*ProcessCommand, error) {
	launch, err := spawn.render()
	if err != nil {
		return nil, err
	}
	path := deps.Executable(launch.Binary)
	return &ProcessCommand{
		ctx: ctx, runner: spawn.runner(), Path: path,
		Args: append([]string{path}, launch.Argv...),
		Env:  launchEnvironment(os.Environ(), launch),
	}, nil
}

func (spawn ClaudeSpawn) runner() deps.Runner {
	if spawn.Runner != nil {
		return spawn.Runner
	}
	return obs.Runner(deps.RealRunner{})
}

func (command *ProcessCommand) start(stdout, stderr io.Writer) (deps.Process, error) {
	return command.runner.Start(command.ctx, command.Args, deps.StartOptions{
		Env: command.Env, Dir: command.Dir, Stdin: command.Stdin,
		Stdout: stdout, Stderr: stderr,
	})
}

func (command *ProcessCommand) Run() error {
	process, err := command.start(command.Stdout, command.Stderr)
	if err != nil {
		return err
	}
	return process.Wait()
}

func (command *ProcessCommand) Output() ([]byte, error) {
	if command.Stdout != nil {
		return nil, errors.New("action: ProcessCommand.Output called with Stdout already set")
	}
	var output bytes.Buffer
	process, err := command.start(&output, command.Stderr)
	if err != nil {
		return nil, err
	}
	if err := process.Wait(); err != nil {
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}

func (spawn ClaudeSpawn) Environment(environ []string) ([]string, error) {
	launch, err := spawn.render()
	if err != nil {
		return nil, err
	}
	return launchEnvironment(environ, launch), nil
}

func launchEnvironment(environ []string, launch claudelaunch.Launch) []string {
	dropped := make(map[string]bool, len(launch.Unset))
	for _, name := range launch.Unset {
		dropped[name] = true
	}
	result := make([]string, 0, len(environ)+len(launch.Env))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !dropped[name] {
			result = append(result, entry)
		}
	}
	return append(result, launch.Env...)
}

func writeAssignments(command *strings.Builder, assignments []string) {
	for _, assignment := range assignments {
		name, value, _ := strings.Cut(assignment, "=")
		command.WriteByte(' ')
		command.WriteString(name)
		command.WriteByte('=')
		command.WriteString(Quote(value))
	}
}
