package mcpserv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
