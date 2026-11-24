package mcpserv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestProbeFamilyRoute(t *testing.T) {
	// The probe's own deadline is read from the process clock.
	fake := probeClock(t)
	for _, test := range []struct {
		name      string
		slow      bool
		cancel    bool
		missing   bool
		blocked   bool
		expired   bool
		wantError string
	}{
		{name: "healthy"},
		{name: "slow healthy", slow: true},
		{name: "caller cancelled", cancel: true, wantError: "context canceled"},
		{name: "unresponsive family", blocked: true, wantError: "context deadline exceeded"},
		{
			name: "earlier caller deadline", blocked: true, expired: true,
			wantError: "context deadline exceeded",
		},
		{name: "legacy route absent", missing: true, wantError: "HTTP 404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The recorder is the process logger; the probe resolves it without the context.
			_, recorder := obs.Test(t)
			// A caller whose deadline passed before the probe's own 2s one.
			caller := testwait.Context(t)
			if test.expired {
				caller = testwait.Expired()
			}
			ctx, cancel := context.WithCancel(caller)
			defer cancel()
			var calls [][]string
			daemon := proxyTestDaemon(proxyTestService("daemon", nil, &calls))
			finished := make(chan struct{})
			asked := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				defer close(finished)
				if test.cancel {
					cancel()
					<-release
					return
				}
				if test.blocked {
					<-release
					return
				}
				if test.slow {
					close(asked)
					select {
					case <-release:
					case <-request.Context().Done():
						return
					}
				}
				if test.missing {
					http.NotFound(writer, request)
					return
				}
				daemon.ServeHTTP(writer, request)
			}))
			defer server.Close()
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			result := make(chan error, 1)
			go func() {
				result <- probeFamilyRoute(ctx, proxyTestAddress(server), pfmconfig.MCPServerChat)
			}()
			armed := func() {
				t.Helper()
				testwait.Until(t, "the route probe's deadline to be armed", func() (bool, string) {
					return fake.Pending() == 1, fake.String()
				})
				if step, _ := fake.NextDue(); step != daemonProbeTimeout {
					t.Fatalf("route probe deadline = %s, want %s: %s", step, daemonProbeTimeout, fake)
				}
			}
			switch {
			case test.slow:
				// The family answers a moment before the probe's deadline.
				testwait.Receive(t, "the route probe to ask the slow family", asked)
				armed()
				fake.Advance(daemonProbeTimeout - time.Nanosecond)
				if pending := fake.Pending(); pending != 1 {
					t.Fatalf("route probe deadline fired %s early: %s", time.Nanosecond, fake)
				}
				unblock.Do(func() { close(release) })
			case test.blocked && !test.expired:
				armed()
				fake.Advance(daemonProbeTimeout)
			}
			err, _ := testwait.Receive(t, "the route probe to answer", result)
			unblock.Do(func() { close(release) })
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("healthy route probe = %v, want success within caller deadline", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("route probe error = %v, want %q", err, test.wantError)
			}
			if test.wantError != "" {
				return
			}
			<-finished
			observed := map[string]int{}
			for _, record := range recorder.Records() {
				if record.Message != "http.in.request" && record.Message != "http.out.request" {
					continue
				}
				status, ok := record.Fields["status"].(float64)
				if !ok || status < 200 || status >= 300 || record.Level != "INFO" {
					t.Errorf("healthy route activity = %+v, want successful INFO HTTP request", record)
				}
				observed[record.Message]++
			}
			for message, want := range map[string]int{"http.in.request": 2, "http.out.request": 1} {
				if observed[message] != want {
					t.Errorf(
						"%s observations = %d, want %d; activity: %s",
						message,
						observed[message],
						want,
						recorder.Raw(),
					)
				}
			}
		})
	}
}
