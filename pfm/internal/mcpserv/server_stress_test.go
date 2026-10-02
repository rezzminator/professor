package mcpserv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStressSequentialCallsNoLeaks(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	_ = callTool[FindOutput](t, client.clientSession, "chat_find", FindInput{
		Excerpt: "alpha unique",
	})
	runtime.GC()
	beforeGoroutines := runtime.NumGoroutine()
	beforeFDs := openFDs(t)
	beforeRSS := rssBytes(t)
	started := time.Now()
	for iteration := 0; iteration < 200; iteration++ {
		output := callTool[FindOutput](
			t,
			client.clientSession,
			"chat_find",
			FindInput{Excerpt: "alpha unique"},
		)
		if output.Count != 1 || output.Candidates[0].ID != "alpha" {
			t.Fatalf("iteration %d cross-talk: %+v", iteration, output)
		}
	}
	elapsed := time.Since(started)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	afterGoroutines := runtime.NumGoroutine()
	afterFDs := openFDs(t)
	afterRSS := rssBytes(t)
	if delta := afterGoroutines - beforeGoroutines; delta > 4 {
		t.Fatalf("goroutine leak: before=%d after=%d", beforeGoroutines, afterGoroutines)
	}
	if delta := afterFDs - beforeFDs; delta > 2 {
		t.Fatalf("fd leak: before=%d after=%d", beforeFDs, afterFDs)
	}
	if delta := afterRSS - beforeRSS; delta > 32<<20 {
		t.Fatalf("RSS grew by %d bytes", delta)
	}
	if os.Getenv("PFM_STRESS_STRICT") == "1" && elapsed > 5*time.Second {
		t.Fatalf("strict sequential MCP stress took %s", elapsed)
	}
	t.Logf(
		"STRESS mcp_sequential calls=200 elapsed_ms=%d goroutines=%d->%d fds=%d->%d rss_bytes=%d->%d",
		elapsed.Milliseconds(),
		beforeGoroutines,
		afterGoroutines,
		beforeFDs,
		afterFDs,
		beforeRSS,
		afterRSS,
	)
}

func TestMCPStressConcurrentEightClientsNoCrossTalk(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	clients := make([]protocolClient, 8)
	for index := range clients {
		clients[index] = connectInMemory(t, service.Server())
	}
	started := time.Now()
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for worker := range clients {
		wait.Add(1)
		go func() {
			defer wait.Done()
			query := "alpha unique"
			want := "alpha"
			if worker%2 == 1 {
				query = "beta unique"
				want = "beta"
			}
			for iteration := 0; iteration < 25; iteration++ {
				ctx, cancel := context.WithTimeout(
					context.Background(),
					15*time.Second,
				)
				result, err := clients[worker].clientSession.CallTool(
					ctx,
					&mcp.CallToolParams{
						Name: "chat_find",
						Arguments: FindInput{
							Excerpt: query,
						},
					},
				)
				cancel()
				if err != nil {
					errors <- fmt.Errorf("worker %d: %w", worker, err)
					return
				}
				content, _ := json.Marshal(result.StructuredContent)
				var output FindOutput
				if err := json.Unmarshal(content, &output); err != nil ||
					output.Count != 1 ||
					output.Candidates[0].ID != want {
					errors <- fmt.Errorf(
						"worker %d cross-talk: %s err=%v",
						worker,
						content,
						err,
					)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	elapsed := time.Since(started)
	if os.Getenv("PFM_STRESS_STRICT") == "1" && elapsed > 8*time.Second {
		t.Fatalf("strict concurrent MCP stress took %s", elapsed)
	}
	t.Logf(
		"STRESS mcp_concurrent clients=8 calls=200 elapsed_ms=%d cross_talk=0",
		elapsed.Milliseconds(),
	)
}

func TestMCPAdversarialUnknownAndHugeArguments(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unknown, unknownErr := client.clientSession.CallTool(
		ctx,
		&mcp.CallToolParams{Name: "unknown_tool"},
	)
	if unknownErr == nil && (unknown == nil || !unknown.IsError) {
		t.Fatalf("unknown tool returned success: result=%+v error=%v", unknown, unknownErr)
	}
	huge := callTool[InjectOutput](t, client.clientSession, "chat_inject", InjectInput{
		Target:  "missing",
		Message: strings.Repeat("x", 1<<20),
	})
	if huge.Code != 4 || huge.Typed || huge.Status != "refused" ||
		!strings.Contains(huge.Message, "matched no live chat") {
		t.Fatalf("huge injection = %+v", huge)
	}
}

func TestMCPMalformedFrameReturnsJSONRPCError(t *testing.T) {
	root := setupBackendFixture(t)
	binary := buildFleetBinary(t)
	command := exec.Command(binary, "--config", writeEnabledMCPConfig(t, root), "mcp", "serve", "--stdio")
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

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot count fds: %v", err)
	}
	return len(entries)
}

func rssBytes(t *testing.T) int64 {
	t.Helper()
	content, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		t.Skipf("cannot read RSS: %v", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		t.Fatalf("malformed statm: %q", content)
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return pages * int64(os.Getpagesize())
}
