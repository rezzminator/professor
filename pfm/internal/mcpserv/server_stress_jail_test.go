//go:build jail

package mcpserv

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMCPMalformedFrameReturnsJSONRPCError(t *testing.T) {
	root := setupBackendPaths(t)
	binary := buildFleetBinary(t)
	command := exec.Command(
		binary,
		"--config",
		writeEnabledMCPConfig(t, root),
		"mcp",
		"serve",
		"--stdio",
		"--family",
		"chat",
	)
	command.Env = os.Environ()
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	if _, err := io.WriteString(
		stdin,
		"{malformed json-rpc}\n"+
			`{"jsonrpc":"2.0","id":7,"method":"unknown/method"}`+"\n",
	); err != nil {
		t.Fatal(err)
	}
	lineChannel := make(chan []string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		lines := make([]string, 0, 2)
		for len(lines) < 2 {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				break
			}
			var envelope struct {
				Method string `json:"method"`
				Error  any    `json:"error"`
			}
			if json.Unmarshal([]byte(line), &envelope) == nil &&
				envelope.Method != "" &&
				envelope.Error == nil {
				// The SDK may publish tools/list_changed between request
				// responses. Notifications have no request ID and are not a
				// malformed-frame response.
				continue
			}
			lines = append(lines, line)
		}
		lineChannel <- lines
	}()
	select {
	case lines := <-lineChannel:
		if len(lines) != 2 {
			t.Fatalf("malformed frame responses = %q", lines)
		}
		for index, line := range lines {
			var response struct {
				JSONRPC string `json:"jsonrpc"`
				Error   any    `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &response); err != nil ||
				response.JSONRPC != "2.0" ||
				response.Error == nil {
				t.Fatalf(
					"malformed frame response[%d] = %q parsed=%+v err=%v stderr=%s",
					index,
					line,
					response,
					err,
					stderr.String(),
				)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("server did not answer malformed frame; stderr=%s", stderr.String())
	}
}
