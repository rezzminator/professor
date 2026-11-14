//go:build jail

package mcpserv

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// With no daemon, the in-process server resolves a Codex caller by its
// _meta.threadId, exactly as the daemon does.
func TestRunStdioResolvesCodexCallerInProcess(t *testing.T) {
	service := metadataIdentityService(t)
	probe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := proxyTestAddress(probe)
	probe.Close()

	var warnings proxyTestBuffer
	session := stdioTestSession(t, stdioTestProfessor(t, service), StdioOptions{
		DaemonAddress: address, Home: service.backend.paths.Home,
		SIDDir: service.backend.paths.SIDDir, Warnings: &warnings,
	})
	whoami := callToolWithMeta[WhoamiOutput](
		t, session, "whoami", mcp.Meta{"threadId": "thread-a"}, WhoamiInput{},
	)
	if whoami.Status != "ok" || whoami.ID != "thread-a" || whoami.Session != "fixture-session" ||
		whoami.Engine != string(pfmengine.Codex) {
		t.Fatalf("in-process whoami = %+v, want thread-a on fixture-session", whoami)
	}
	if !strings.Contains(warnings.String(), "daemon absent at "+address) {
		t.Fatalf("warnings = %q, want the absent-daemon fallback", warnings.String())
	}
}

func TestRunStdioUsesHealthyDaemonProxy(t *testing.T) {
	var daemonWarnings, localWarnings bytes.Buffer
	daemon := stdioTestService("daemon", &daemonWarnings)
	server := httptest.NewServer(proxyTestDaemon(daemon))
	defer server.Close()

	local := stdioTestService("local", &localWarnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local.backend.paths = resolved
	address := proxyTestAddress(server)
	markerPath := filepath.Join(local.backend.paths.Home, ".local", "state", "pfm", "mcp-proxy-compatible")
	inspect := func() {
		markerTarget, err := filepath.EvalSymlinks(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		links, err := gather.NewProcFS("").FDLinks(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			if link.Target == markerTarget {
				return
			}
		}
		t.Fatalf("healthy daemon proxy does not hold marker %s", markerPath)
	}
	if marker := stdioTestRunInspect(t, local, address, inspect).Message; marker != "daemon" {
		t.Fatalf("healthy daemon marker = %q, want proxy daemon marker", marker)
	}
	links, err := gather.NewProcFS("").FDLinks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	markerTarget, err := filepath.EvalSymlinks(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Target == markerTarget {
			t.Fatalf("proxy marker descriptor %d remained open after proxy returned", link.FD)
		}
	}
}
