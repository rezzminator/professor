package mockengine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
)

func openCodeHeadlessRequest(fix *fixture, prompt string) run.Request {
	home := filepath.Join(fix.root, "opencode")
	if err := os.MkdirAll(home, 0o700); err != nil {
		fix.t.Fatal(err)
	}
	return run.Request{
		Config: pfmconfig.Config{
			OpenCode:         pfmconfig.OpenCodePrefs{Binary: filepath.Join(fix.bin, "opencode")},
			OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: home}},
		},
		Engine: pfmengine.OpenCode, Prompt: prompt, CWD: fix.work,
		TempDir: fix.root, Timeout: 5 * time.Second,
	}
}

func TestOpenCodeServeDeletesSeedSession(t *testing.T) {
	state := &openCodeServerState{proc: &process{env: func(string) string { return "" }}}
	created := httptest.NewRecorder()
	state.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/session", http.NoBody))
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d", created.Code)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{session.ID, session.ID, "unknown"} {
		response := httptest.NewRecorder()
		state.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/session/"+id, http.NoBody))
		if i == 0 {
			if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "true" {
				t.Fatalf("delete status=%d body=%q", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusNotFound {
			t.Fatalf("repeat %d status=%d", i, response.Code)
		}
	}
}

func TestOpenCodeServeRejectsWrongBasicCredentials(t *testing.T) {
	state := &openCodeServerState{proc: &process{env: func(name string) string {
		switch name {
		case "OPENCODE_SERVER_USERNAME":
			return "alice"
		case "OPENCODE_SERVER_PASSWORD":
			return "secret"
		}
		return ""
	}}}
	request := httptest.NewRequest(http.MethodGet, "/global/health", http.NoBody)
	for _, credentials := range []bool{false, true} {
		if credentials {
			request.SetBasicAuth("alice", "wrong")
		}
		response := httptest.NewRecorder()
		state.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("credentials=%v status=%d", credentials, response.Code)
		}
	}
}
