package statusline

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestReadCodexRateLimitsCommandRecordsTheAppServerProcess: the Codex App
// Server command is one of the four direct process doors — its start and
// exit write comp=runner records with the binary's shape and pid, and the
// JSON-RPC payload never reaches the file.
func TestReadCodexRateLimitsCommandRecordsTheAppServerProcess(t *testing.T) {
	ctx, recorder := obs.Test(t)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCodexAppServerFixture", "--", "app-server")
	command.Env = append(os.Environ(), appServerFixtureEnv+"=1")
	if _, err := readCodexRateLimitsCommand(ctx, command); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "runner.start" || records[1].Message != "runner.exit" {
		t.Fatalf("want runner.start then runner.exit, got %s", recorder.Raw())
	}
	for _, record := range records {
		if comp, _ := record.Field(obs.FieldComp); comp != "runner" {
			t.Fatalf("%s comp = %v", record.Message, comp)
		}
		if pid, found := record.Field(obs.FieldPID); !found || pid == float64(0) {
			t.Fatalf("%s pid = %v", record.Message, pid)
		}
	}
	if argc, _ := records[0].Field("argc"); argc != float64(4) {
		t.Fatalf("argc = %v, want 4", argc)
	}
	if _, found := records[1].Field(obs.FieldDur); !found {
		t.Fatalf("exit record has no dur_ms: %v", records[1].Fields)
	}
	if raw := recorder.Raw(); containsAny(raw, "jsonrpc", "rateLimits") {
		t.Fatalf("the handshake payload reached the file: %s", raw)
	}
	_ = context.Background
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if len(needle) != 0 && len(text) >= len(needle) && indexOf(text, needle) {
			return true
		}
	}
	return false
}

func indexOf(text, needle string) bool {
	for index := 0; index+len(needle) <= len(text); index++ {
		if text[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
