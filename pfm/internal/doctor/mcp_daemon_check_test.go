package doctor

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// useRealDaemonReachability opts a test back into the real
// mcpserv.DaemonReachability probe (against its own listener/httptest
// server) instead of the jailed TestMain stub, and restores the stub after
// the test so a later test in the same package never probes a live daemon.
func useRealDaemonReachability(t *testing.T) {
	t.Helper()
	previous := DaemonReachabilityOverride
	DaemonReachabilityOverride = nil
	t.Cleanup(func() { DaemonReachabilityOverride = previous })
}

func runtimeForPort(port int) pfmconfig.Runtime {
	return pfmconfig.Runtime{
		Version: "v1.0.0",
		Config: pfmconfig.Config{
			MCP:        pfmconfig.MCPConfig{HTTP: pfmconfig.MCPHTTP{Port: port}},
			MCPServers: map[string]pfmconfig.MCPServer{"chat": {Enabled: true}},
		},
	}
}

func runtimeForDisabledMCPPort(port int) pfmconfig.Runtime {
	runtime := runtimeForPort(port)
	runtime.Config.MCPServers = map[string]pfmconfig.MCPServer{
		"chat":                       {Enabled: false},
		pfmconfig.MCPServerHarvester: {Enabled: false},
	}
	return runtime
}

// TestMCPDaemonDoctorNamesUnreachableWhenNothingListens is the "our daemon
// is down" case: nothing answers the configured port at all.
func TestMCPDaemonDoctorNamesUnreachableWhenNothingListens(t *testing.T) {
	useRealDaemonReachability(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForPort(port))
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1", warnings)
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=unreachable") {
		t.Fatalf("output = %q, want an unreachable row", output.String())
	}
	if strings.Contains(output.String(), "foreign-service") {
		t.Fatalf("output = %q, an absent daemon must never read as a foreign service", output.String())
	}
}

// TestMCPDaemonDoctorNamesAForeignServiceDistinctlyFromUnreachable pins the
// fix: a listener on the configured port that answers, but not as pfm's own
// daemon (here: HTTP 500), must get its OWN row — never the same
// "unreachable" line an absent daemon gets, which would misread as "our
// daemon is down" when something else entirely is squatting the port.
func TestMCPDaemonDoctorNamesAForeignServiceDistinctlyFromUnreachable(t *testing.T) {
	useRealDaemonReachability(t)
	foreign := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer foreign.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(foreign.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForPort(port))
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1", warnings)
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=foreign-service") {
		t.Fatalf("output = %q, want a foreign-service row", output.String())
	}
	if strings.Contains(output.String(), "daemon=unreachable") {
		t.Fatalf("output = %q, a foreign service must never read as our daemon being down", output.String())
	}
}

// TestMCPDaemonDoctorReportsRunningWithNoWarnings is the control: a healthy,
// matching-version daemon gets the plain running row and no warning.
func TestMCPDaemonDoctorReportsRunningWithNoWarnings(t *testing.T) {
	useRealDaemonReachability(t)
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"pfmVersion":"v1.0.0","protocolVersion":"1","pid":1234,"startTime":"now","endpoint":"http://127.0.0.1"}`,
		))
	}))
	defer healthy.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(healthy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForPort(port))
	if warnings != 0 {
		t.Fatalf("warnings=%d, want 0\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=running pid=1234") {
		t.Fatalf("output = %q, want the running row", output.String())
	}
}

func TestMCPDaemonDoctorReportsDisabledConfigWhenNothingListens(t *testing.T) {
	useRealDaemonReachability(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForDisabledMCPPort(port))
	if warnings != 0 {
		t.Fatalf("warnings=%d, want 0\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=unreachable disabled-in-config") {
		t.Fatalf("output = %q, want the disabled-in-config unreachable row", output.String())
	}
}

func TestMCPDaemonDoctorReportsRunningWhenConfigIsDisabled(t *testing.T) {
	useRealDaemonReachability(t)
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"pfmVersion":"v1.0.0","protocolVersion":"1","pid":1234,"startTime":"now","endpoint":"http://127.0.0.1"}`,
		))
	}))
	defer healthy.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(healthy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForDisabledMCPPort(port))
	if warnings != 0 {
		t.Fatalf("warnings=%d, want 0\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=running pid=1234") {
		t.Fatalf("output = %q, want the running row despite disabled config", output.String())
	}
}

func TestDoctorPrintsDaemonAndServeRowsWhenMCPConfigIsDisabled(t *testing.T) {
	useRealDaemonReachability(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	runtime := buildCleanDoctorHome(t)
	runtime.Config.MCP.HTTP.Port = port
	runtime.Config.MCPServers = runtimeForDisabledMCPPort(port).Config.MCPServers

	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	for _, want := range []string{
		"doctor: mcp daemon=unreachable disabled-in-config",
		"doctor: mcp-serve clean checked=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

// TestMCPDaemonDoctorWarnsOnVersionSkew pins the version-skew warning: a
// running daemon that answers as itself but reports a build different from
// this binary's still gets the "running" row, plus its own dedicated
// version-skew warning line — never silently treated as a plain match.
func TestMCPDaemonDoctorWarnsOnVersionSkew(t *testing.T) {
	useRealDaemonReachability(t)
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"pfmVersion":"v9.9.9-skewed","protocolVersion":"1","pid":1234,"startTime":"now","endpoint":"http://127.0.0.1"}`,
		))
	}))
	defer healthy.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(healthy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := printMCPDaemonDoctor(&output, runtimeForPort(port))
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=running pid=1234") {
		t.Fatalf("output = %q, want the running row despite version skew", output.String())
	}
	if !strings.Contains(output.String(), "doctor: mcp daemon=version-skew daemon=v9.9.9-skewed client=v1.0.0") {
		t.Fatalf("output = %q, want a version-skew warning naming both versions", output.String())
	}
}
