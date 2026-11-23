package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestMCPStressSequentialCallsNoLeaks(t *testing.T) {
	setupBackendPaths(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	_ = callTool[LastOutput](t, client.clientSession, "last", LastInput{
		Target: "alpha",
	})
	runtime.GC()
	beforeGoroutines := runtime.NumGoroutine()
	beforeFDs := openFDs(t)
	beforeRSS := rssBytes(t)
	for iteration := 0; iteration < 200; iteration++ {
		output := callTool[LastOutput](
			t,
			client.clientSession,
			"last",
			LastInput{Target: "alpha"},
		)
		if output.Text != "alpha answer" {
			t.Fatalf("iteration %d cross-talk: %+v", iteration, output)
		}
	}
	var afterGoroutines int
	testwait.Until(t, "the call goroutines to wind down", func() (bool, string) {
		runtime.GC()
		afterGoroutines = runtime.NumGoroutine()
		return afterGoroutines-beforeGoroutines <= 4, fmt.Sprintf(
			"goroutines before=%d after=%d",
			beforeGoroutines,
			afterGoroutines,
		)
	})
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
	t.Logf(
		"STRESS mcp_sequential calls=200 goroutines=%d->%d fds=%d->%d rss_bytes=%d->%d",
		beforeGoroutines,
		afterGoroutines,
		beforeFDs,
		afterFDs,
		beforeRSS,
		afterRSS,
	)
}

func TestMCPStressConcurrentEightClientsNoCrossTalk(t *testing.T) {
	setupBackendPaths(t)
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
	callContext := testwait.Context(t)
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for worker := range clients {
		wait.Add(1)
		go func() {
			defer wait.Done()
			query := "alpha"
			want := "alpha"
			if worker%2 == 1 {
				query = "beta"
				want = "beta"
			}
			for iteration := 0; iteration < 25; iteration++ {
				ctx, cancel := context.WithCancel(callContext)
				result, err := clients[worker].clientSession.CallTool(
					ctx,
					&mcp.CallToolParams{
						Name: "last",
						Arguments: LastInput{
							Target: query,
						},
					},
				)
				cancel()
				if err != nil {
					errors <- fmt.Errorf("worker %d: %w", worker, err)
					return
				}
				content, _ := json.Marshal(result.StructuredContent)
				var output LastOutput
				if err := json.Unmarshal(content, &output); err != nil ||
					output.Text != want+" answer" {
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
	t.Log("STRESS mcp_concurrent clients=8 calls=200 cross_talk=0")
}

// BenchmarkMCPSequentialCalls measures what the sequential stress test no
// longer judges: one "last" call over an in-memory session.
func BenchmarkMCPSequentialCalls(b *testing.B) {
	setupBackendPaths(b)
	service := newFixtureService(b)
	b.Cleanup(func() {
		if err := service.Close(); err != nil {
			b.Errorf("close service: %v", err)
		}
	})
	client := connectInMemory(b, service.Server())
	for b.Loop() {
		output := callTool[LastOutput](b, client.clientSession, "last", LastInput{Target: "alpha"})
		if output.Text != "alpha answer" {
			b.Fatalf("cross-talk: %+v", output)
		}
	}
}

// BenchmarkMCPConcurrentEightClients measures what the concurrent stress test
// no longer judges: "last" calls from eight in-memory sessions at once.
func BenchmarkMCPConcurrentEightClients(b *testing.B) {
	setupBackendPaths(b)
	service := newFixtureService(b)
	b.Cleanup(func() {
		if err := service.Close(); err != nil {
			b.Errorf("close service: %v", err)
		}
	})
	clients := make([]protocolClient, 8)
	for index := range clients {
		clients[index] = connectInMemory(b, service.Server())
	}
	callContext := testwait.Context(b)
	for b.Loop() {
		var wait sync.WaitGroup
		failures := make(chan error, len(clients))
		for _, client := range clients {
			wait.Go(func() {
				result, err := client.clientSession.CallTool(callContext, &mcp.CallToolParams{
					Name: "last", Arguments: LastInput{Target: "beta"},
				})
				if err != nil {
					failures <- err
					return
				}
				content, _ := json.Marshal(result.StructuredContent)
				var output LastOutput
				if err := json.Unmarshal(content, &output); err != nil || output.Text != "beta answer" {
					failures <- fmt.Errorf("cross-talk: %s err=%v", content, err)
				}
			})
		}
		wait.Wait()
		close(failures)
		for err := range failures {
			b.Fatal(err)
		}
	}
}

func TestMCPAdversarialUnknownAndHugeArguments(t *testing.T) {
	setupBackendPaths(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	unknown, unknownErr := client.clientSession.CallTool(
		ctx,
		&mcp.CallToolParams{Name: "unknown_tool"},
	)
	if unknownErr == nil && (unknown == nil || !unknown.IsError) {
		t.Fatalf("unknown tool returned success: result=%+v error=%v", unknown, unknownErr)
	}
	huge := callTool[CallOutput](t, client.clientSession, "call", CallInput{
		Target:  "missing",
		Message: strings.Repeat("x", 1<<20),
	})
	if huge.Code != 4 || huge.Typed || huge.Status != "not_found" || huge.Message != `no chat named "missing"` {
		t.Fatalf("huge call = %+v", huge)
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(paths.SystemProcRoot(), "self", "fd"))
	if err != nil {
		t.Skipf("cannot count fds: %v", err)
	}
	return len(entries)
}

func rssBytes(t *testing.T) int64 {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(paths.SystemProcRoot(), "self", "statm"))
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
