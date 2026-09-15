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

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
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
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "harvester") {
		t.Fatalf("authenticated /mcp = %d %s", response.StatusCode, body)
	}
	for _, path := range []string{"/mcp/chat", "/mcp/harvester", "/status"} {
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
	handler := newMCPDaemonHandler(mcpDaemonOptions{Version: "test", External: &state})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
	var status mcpDaemonStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.HarvesterExternal != failed {
		t.Fatalf("status harvesterExternal = %q, want %q", status.HarvesterExternal, failed)
	}
}

// A stale registration that passes a retired flag fails loudly with the key
// that replaced it.
func TestHarvesterServeRetiredFlagsNameTheirConfigKey(t *testing.T) {
	cases := map[string]string{
		"--user-agent=UA":         "fetch.userAgent",
		"--port":                  "external.port",
		"--internal-port":         "loopback port",
		"--allow-unauthenticated": "always authenticates",
	}
	for flag, want := range cases {
		var stdout, stderr bytes.Buffer
		if code := runHarvesterMCP(
			[]string{flag},
			&stdout,
			&stderr,
			commandRuntime{},
		); code != 2 ||
			!strings.Contains(stderr.String(), want) {
			t.Errorf("serve %s: code=%d stderr=%q, want 2 naming %q", flag, code, stderr.String(), want)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runHarvesterMCP(
		[]string{"--transport", "http"},
		&stdout,
		&stderr,
		commandRuntime{},
	); code != 2 ||
		!strings.Contains(stderr.String(), "pfm mcp serve") {
		t.Fatalf("--transport http: code=%d stderr=%q", code, stderr.String())
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
		applied,
	); options.MCPPort != config.DefaultMCPPort {
		t.Fatalf("installer would wire port %d, want %d", options.MCPPort, config.DefaultMCPPort)
	}
}

// A retired harvester variable still exported, or a pre-split layout not yet
// migrated, is a setting that silently stopped applying — doctor warns on each.
func TestDoctorWarnsOnRetiredHarvesterEnvAndPreSplitLayout(t *testing.T) {
	clearRetiredHarvesterEnv(t)
	t.Setenv("SEARXNG_URL", "http://127.0.0.1:8888")
	t.Setenv("HARVESTER_LOCAL_ROOTS", "/srv")
	runtime := commandRuntime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.Path = filepath.Join(t.TempDir(), config.LegacyFileName)
	runtime.Config.Harvester.Path = filepath.Join(filepath.Dir(runtime.Config.Path), config.HarvesterFileName)
	runtime.Config.Exists = true
	if err := os.WriteFile(runtime.Config.Path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if warnings := printHarvesterConfigDoctor(&stdout, runtime); warnings != 3 {
		t.Fatalf("warnings=%d, want 3 (layout + two retired variables)\n%s", warnings, stdout.String())
	}
	for _, want := range []string{"layout=pre-split", "retired_env=SEARXNG_URL", "search.searxngURL", "retired_env=HARVESTER_LOCAL_ROOTS", "never honored"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("doctor output lacks %q:\n%s", want, stdout.String())
		}
	}
}

// A configured external gateway always renders a line: its live state, or why
// it cannot be running. Only "listening" is healthy.
func TestDoctorExternalGatewayNeverRendersAsAbsence(t *testing.T) {
	cases := []struct {
		name, reported, want        string
		enabled, external, warnings int
	}{
		{name: "off", enabled: 1, external: 0, warnings: 0, want: ""},
		{
			name:     "harvester disabled",
			enabled:  0,
			external: 1,
			reported: "disabled",
			warnings: 1,
			want:     "harvester.enabled is false",
		},
		{name: "old daemon", enabled: 1, external: 1, warnings: 1, want: "not reported"},
		{
			name:     "failed",
			enabled:  1,
			external: 1,
			reported: "failed: listen tcp 127.0.0.1:18378: bind",
			warnings: 1,
			want:     "failed: listen",
		},
		{
			name:     "listening",
			enabled:  1,
			external: 1,
			reported: "listening on 127.0.0.1:18378",
			warnings: 0,
			want:     "listening on",
		},
	}
	for _, tc := range cases {
		harvester := config.DefaultHarvester()
		harvester.Enabled = tc.enabled == 1
		harvester.External.Enabled = tc.external == 1
		var stdout bytes.Buffer
		warnings := printHarvesterExternalDoctor(&stdout, harvester, tc.reported)
		if warnings != tc.warnings {
			t.Errorf("%s: warnings=%d, want %d (%q)", tc.name, warnings, tc.warnings, stdout.String())
		}
		if tc.want == "" && stdout.Len() != 0 || tc.want != "" && !strings.Contains(stdout.String(), tc.want) {
			t.Errorf("%s: output %q, want %q", tc.name, stdout.String(), tc.want)
		}
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
