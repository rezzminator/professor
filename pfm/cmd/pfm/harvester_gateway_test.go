package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func externalRuntime(t *testing.T, port int) commandRuntime {
	t.Helper()
	home := t.TempDir()
	runtime := commandRuntime{Config: config.Defaults(home, nil)}
	runtime.Paths.Home = home
	runtime.Config.Harvester.Enabled = true
	runtime.Config.Harvester.Cache.Dir = filepath.Join(home, "cache")
	runtime.Config.Harvester.External = config.HarvesterExternal{
		Enabled: true, Host: "127.0.0.1", Port: port, PublicURL: "https://harvester.example.test",
		StaticToken: "example-gateway-token", StateDir: filepath.Join(home, "state"),
	}
	return runtime
}

// The external gateway is the second port of the ONE daemon process: it
// serves the harvester behind the auth wall and never the chat MCP.
func TestHarvesterExternalGatewayServesHarvesterBehindAuthOnly(t *testing.T) {
	port := freeLoopbackPort(t)
	runtime := externalRuntime(t, port)
	var state atomic.Pointer[string]
	stop, err := startHarvesterExternal(runtime, io.Discard, func(value string) { state.Store(&value) })
	if err != nil {
		t.Fatalf("startHarvesterExternal: %v", err)
	}
	defer stop()
	if got := *state.Load(); !strings.HasPrefix(got, "listening on 127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("external state = %q", got)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	sessionID := ""
	do := func(method, path, token, body string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "harvester.example.test"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		if sessionID != "" {
			request.Header.Set("Mcp-Session-Id", sessionID)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	if response := do(http.MethodPost, "/mcp", "", initialize); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp = %d, want 401", response.StatusCode)
	}
	if response := do(http.MethodPost, "/mcp", "wrong", initialize); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token /mcp = %d, want 401", response.StatusCode)
	}
	response := do(http.MethodPost, "/mcp", "example-gateway-token", initialize)
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "harvester_read") ||
		!strings.Contains(string(body), `"name":"harvester"`) {
		t.Fatalf("authenticated /mcp = %d %s, want serverInfo harvester and harvester_read", response.StatusCode, body)
	}
	sessionID = response.Header.Get("Mcp-Session-Id")
	do(http.MethodPost, "/mcp", "example-gateway-token", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	listed := do(http.MethodPost, "/mcp", "example-gateway-token", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var tools struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	listBody, _ := io.ReadAll(listed.Body)
	// The gateway answers as a server-sent event: the JSON-RPC reply is its data line.
	payload := string(listBody)
	for _, line := range strings.Split(payload, "\n") {
		if data, found := strings.CutPrefix(line, "data: "); found {
			payload = data
		}
	}
	if err := json.Unmarshal([]byte(payload), &tools); err != nil || len(tools.Result.Tools) == 0 {
		t.Fatalf("external tools/list = %d %s (decode: %v)", listed.StatusCode, listBody, err)
	}
	sawRead := false
	for _, tool := range tools.Result.Tools {
		if !strings.HasPrefix(tool.Name, "harvester_") {
			t.Fatalf("external gateway lists %q, want harvester_* only: %s", tool.Name, listBody)
		}
		if tool.Name == "harvester_read" {
			sawRead = true
			if _, hasFiles := tool.InputSchema.Properties["files"]; hasFiles {
				t.Fatalf("external harvester_read takes files: %s", listBody)
			}
		}
	}
	if !sawRead {
		t.Fatalf("external tools/list has no harvester_read: %s", listBody)
	}
	sessionID = ""
	for _, path := range []string{"/mcp/professor", "/mcp/professor/chat", "/mcp/professor/harvester", "/status"} {
		if response := do(
			http.MethodPost,
			path,
			"example-gateway-token",
			initialize,
		); response.StatusCode != http.StatusNotFound {
			t.Errorf(
				"external %s = %d, want 404 — the external port serves the harvester only",
				path,
				response.StatusCode,
			)
		}
	}
}

// A port the external gateway cannot bind is a reported failure, never a
// silent absence — and it must not claim to be listening.
func TestHarvesterExternalGatewayReportsBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Errorf("close occupied: %v", err)
		}
	}()
	runtime := externalRuntime(t, occupied.Addr().(*net.TCPAddr).Port)
	var state atomic.Pointer[string]
	stop, err := startHarvesterExternal(runtime, io.Discard, func(value string) { state.Store(&value) })
	if err == nil {
		stop()
		t.Fatal("external gateway bound an occupied port")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Fatalf("bind failure error = %v", err)
	}
	if got := state.Load(); got != nil {
		t.Fatalf("a failed bind set state %q", *got)
	}
}

// /status carries the external gateway's live state so doctor can report it.
func TestMCPDaemonStatusReportsHarvesterExternalState(t *testing.T) {
	var state atomic.Pointer[string]
	failed := "failed: listen 127.0.0.1:18378: address already in use"
	state.Store(&failed)
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{Version: "test", External: &state})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
	var status mcpserv.DaemonStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.HarvesterExternal != failed {
		t.Fatalf("status harvesterExternal = %q, want %q", status.HarvesterExternal, failed)
	}
}

// pfm install migrates a pre-split machine BEFORE it wires clients: the
// preview wires the migrated port and changes nothing on disk; the apply
// renames, splits, moves the port, and reloads.
func TestInstallMigratesPreSplitConfigBeforeWiring(t *testing.T) {
	jailTest(t)
	home := os.Getenv("PFM_HOME")
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "pfm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, config.LegacyFileName)
	if err := os.WriteFile(
		legacy,
		[]byte(
			`{"version":2,"mcp":{"http":{"port":8377},"servers":{"chat":{"enabled":true},"harvester":{"enabled":true}}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	preview, code := migrateMachineConfig(installer.ModeDryRun, &stdout, &stderr, runtime)
	if code != 0 || preview.Config.MCP.HTTP.Port != config.DefaultMCPPort ||
		!strings.Contains(stdout.String(), "change  rename") {
		t.Fatalf(
			"preview code=%d port=%d stdout=%q stderr=%q",
			code,
			preview.Config.MCP.HTTP.Port,
			stdout.String(),
			stderr.String(),
		)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("preview touched the pre-split file: %v", err)
	}
	applied, code := migrateMachineConfig(installer.ModeApply, &stdout, &stderr, runtime)
	if code != 0 {
		t.Fatalf("apply code=%d stderr=%q", code, stderr.String())
	}
	if applied.Config.Path != filepath.Join(dir, config.FileName) ||
		applied.Config.MCP.HTTP.Port != config.DefaultMCPPort ||
		!applied.Config.Harvester.Enabled ||
		applied.Config.MCPServerSource("harvester") != config.SourceFile {
		t.Fatalf("applied path=%q port=%d harvester=%t source=%q", applied.Config.Path, applied.Config.MCP.HTTP.Port,
			applied.Config.Harvester.Enabled, applied.Config.MCPServerSource("harvester"))
	}
	if options := newInstallerOptions(
		installer.ModeApply,
		"",
		true,
		io.Discard,
		io.Discard,
		applied,
	); options.MCPPort != config.DefaultMCPPort {
		t.Fatalf("installer would wire port %d, want %d", options.MCPPort, config.DefaultMCPPort)
	}
}

// Without --force, init refuses before writing either file, so it never leaves
// a fresh pfm.config.json beside a harvester file it refused to touch.
func TestConfigInitRefusesBeforeWritingEitherFile(t *testing.T) {
	dir := t.TempDir()
	runtime := commandRuntime{Config: config.Defaults(dir, nil)}
	runtime.Paths.Home = dir
	runtime.Config.Path = filepath.Join(dir, config.FileName)
	harvesterPath := config.HarvesterPath(runtime.Config.Path)
	if err := os.WriteFile(harvesterPath, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runConfigInit(
		nil,
		&stdout,
		&stderr,
		runtime,
	); code != 1 ||
		!strings.Contains(stderr.String(), harvesterPath) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(runtime.Config.Path); !os.IsNotExist(err) {
		t.Fatalf("init wrote %s before refusing: %v", runtime.Config.Path, err)
	}
}
