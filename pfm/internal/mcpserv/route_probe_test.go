package mcpserv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestProbeFamilyRoute(t *testing.T) {
	for _, test := range []struct {
		name      string
		delay     time.Duration
		cancel    bool
		missing   bool
		blocked   bool
		deadline  time.Duration
		wantError string
	}{
		{name: "healthy"},
		{name: "slow healthy", delay: 1100 * time.Millisecond},
		{name: "caller cancelled", cancel: true, wantError: "context canceled"},
		{name: "unresponsive family", blocked: true, wantError: "context deadline exceeded"},
		{
			name: "earlier caller deadline", blocked: true, deadline: 50 * time.Millisecond,
			wantError: "context deadline exceeded",
		},
		{name: "legacy route absent", missing: true, wantError: "HTTP 404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			deadline := 5 * time.Second
			if test.deadline > 0 {
				deadline = test.deadline
			}
			ctx, cancel := context.WithTimeout(ctx, deadline)
			defer cancel()
			var calls [][]string
			daemon := proxyTestDaemon(proxyTestService("daemon", nil, &calls))
			finished := make(chan struct{})
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
				if test.delay > 0 {
					timer := time.NewTimer(test.delay)
					defer timer.Stop()
					select {
					case <-timer.C:
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
			result := make(chan error, 1)
			go func() {
				result <- probeFamilyRoute(ctx, proxyTestAddress(server), pfmconfig.MCPServerChat)
			}()
			watchdog := time.NewTimer(daemonProbeTimeout + time.Second)
			defer watchdog.Stop()
			var err error
			select {
			case err = <-result:
				close(release)
			case <-watchdog.C:
				close(release)
				err = <-result
				t.Fatalf(
					"route probe exceeded daemon probe bound %s; returned %v after handler release",
					daemonProbeTimeout,
					err,
				)
			}
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
