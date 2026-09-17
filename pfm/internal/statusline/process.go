package statusline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
)

// SpawnDetached starts one refresher as a new session and releases the child.
// The render path never waits for credentials, networks, or App Server startup.
func SpawnDetached(kind RefreshKind) (returnErr error) {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve pfm executable: %w", err)
	}
	argument := ""
	switch kind {
	case RefreshKindCodex:
		argument = "--refresh-gpt"
	default:
		return fmt.Errorf("unknown statusline refresher %q", kind)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device: %w", err)
	}
	defer func() {
		if err := null.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close null device: %w", err))
		}
	}()
	command := exec.Command(executable, "statusline", argument)
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start detached %s refresher: %w", kind, err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release detached %s refresher: %w", kind, err)
	}
	return nil
}

// ReadCodexRateLimits performs the complete Codex App Server initialize exchange
// and returns its id=1 response. It runs only in a detached refresher child.
func ReadCodexRateLimits(ctx context.Context) ([]byte, error) {
	return ReadCodexRateLimitsWithBinary(ctx, pfmengine.MustLookup(pfmengine.Codex).Binary)
}

// ReadCodexRateLimitsWithBinary uses the machine-configured Codex command.
func ReadCodexRateLimitsWithBinary(ctx context.Context, binary string) ([]byte, error) {
	return ReadCodexRateLimitsWithBinaryAtHome(ctx, binary, "")
}

// ReadCodexRateLimitsWithBinaryAtHome reads one configured Codex account. An
// explicit home keeps multi-account limits isolated from the caller's own
// CODEX_HOME.
func ReadCodexRateLimitsWithBinaryAtHome(ctx context.Context, binary, codexHome string) ([]byte, error) {
	if binary == "" {
		binary = pfmengine.MustLookup(pfmengine.Codex).Binary
	}
	child, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	command := exec.CommandContext(child, deps.Executable(binary), "app-server")
	if codexHome != "" {
		command.Env = replaceCommandEnv(os.Environ(), "CODEX_HOME", codexHome)
	}
	return readCodexRateLimitsCommand(command)
}

func replaceCommandEnv(environment []string, name, value string) []string {
	prefix := name + "="
	replaced := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			replaced = append(replaced, entry)
		}
	}
	return append(replaced, prefix+value)
}

func readCodexRateLimitsCommand(command *exec.Cmd) ([]byte, error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open App Server stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open App Server stdout: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Codex App Server: %w", err)
	}
	payload := strings.Join([]string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"clientInfo":{"name":"statusline","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"account/rateLimits/read","params":null}`,
	}, "\n") + "\n"
	if _, err := io.WriteString(stdin, payload); err != nil {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("write App Server handshake: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(line, &envelope) == nil && string(envelope.ID) == "1" {
			_ = stdin.Close()
			_ = command.Process.Kill()
			_ = command.Wait()
			return append(line, '\n'), nil
		}
	}
	_ = stdin.Close()
	if err := scanner.Err(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("read App Server response: %w", err)
	}
	waitErr := command.Wait()
	return nil, fmt.Errorf(
		"command from App Server returned no id=1 response: wait=%v stderr=%s",
		waitErr,
		strings.TrimSpace(stderr.String()),
	)
}
