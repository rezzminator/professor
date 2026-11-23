package mcpserv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

type proxyTestHarness struct {
	input  *io.PipeWriter
	output *bufio.Reader
	cancel context.CancelFunc
	done   chan error
}

// proxySteppingClock is a proxy's clock in a test that hands it no clock of
// its own: a wait the proxy makes (a retry delay, a request's admission
// window) passes at once and moves the fake forward by its length, while a
// bound (the retry window, the daemon probe's deadline, the session close's
// deadline) fires only when the fake reaches it. A wait that would outlast a pending bound moves the fake to
// that bound instead and stays unfired, so the proxy meets the bound's end, as
// it would on the real clock.
type proxySteppingClock struct {
	*clock.Fake
	proxy *stdioProxy
}

func (c proxySteppingClock) NewTimer(delay time.Duration) clock.Timer {
	if delay == c.proxy.retryWindow || delay == daemonProbeTimeout || delay == proxyCloseTimeout {
		return c.Fake.NewTimer(delay)
	}
	if step, due := c.NextDue(); due && step <= delay {
		c.Advance(step)
		return c.Fake.NewTimer(delay)
	}
	timer := c.Fake.NewTimer(delay)
	c.Advance(delay)
	return timer
}

// proxyTestStepping puts proxy on a proxySteppingClock and returns its fake.
func proxyTestStepping(proxy *stdioProxy) *clock.Fake {
	fake := clock.NewFake(time.Unix(0, 0))
	proxy.clock = proxySteppingClock{Fake: fake, proxy: proxy}
	return fake
}

func startProxyTestHarness(t *testing.T, proxy *stdioProxy) *proxyTestHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	input, inputWriter := io.Pipe()
	output, outputWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- proxy.run(ctx, input, outputWriter)
		_ = outputWriter.Close()
	}()
	harness := &proxyTestHarness{
		input: inputWriter, output: bufio.NewReader(output), cancel: cancel, done: done,
	}
	t.Cleanup(func() {
		_ = inputWriter.Close()
		go func() { _, _ = io.Copy(io.Discard, harness.output) }()
		_, _ = testwait.Receive(t, "the stdio proxy to stop after cancellation", done)
		cancel()
	})
	return harness
}

func (harness *proxyTestHarness) write(t *testing.T, frame string) {
	t.Helper()
	if _, err := io.WriteString(harness.input, frame+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (harness *proxyTestHarness) read(t *testing.T) map[string]any {
	t.Helper()
	type answer struct {
		line []byte
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		line, err := harness.output.ReadBytes('\n')
		result <- answer{line, err}
	}()
	got, _ := testwait.Receive(t, "the proxy's response line", result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	var frame map[string]any
	if err := json.Unmarshal(got.line, &frame); err != nil {
		t.Fatalf("decode proxy response %s: %v", got.line, err)
	}
	return frame
}

func TestProxyHarnessClosesDaemonSessionBeforeCleanupReturns(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		// expire moves the proxy's clock past its close bound while the daemon holds the DELETE.
		expire      bool
		wantWarning string
	}{
		{name: "a DELETE the daemon answers inside the close bound"},
		{
			name: "a DELETE held past the close bound", expire: true,
			wantWarning: `pfm mcp stdio proxy: close daemon session "session": Delete "{url}": context deadline exceeded` + "\n",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			entered := make(chan struct{})
			release := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodDelete {
					t.Errorf("method=%s, want DELETE", request.Method)
				}
				close(entered)
				<-release
				writer.WriteHeader(http.StatusNoContent)
				close(closed)
			}))
			defer server.Close()

			var warnings proxyTestBuffer
			fake := clock.NewFake(time.Unix(0, 0))
			returned := make(chan struct{})
			go func() {
				defer close(returned)
				t.Run("proxy", func(t *testing.T) {
					proxy := &stdioProxy{
						endpoint: server.URL, client: obs.WrapClient(&http.Client{}), warnings: &warnings,
						sessionID: "session", clock: fake,
					}
					startProxyTestHarness(t, proxy)
				})
			}()
			testwait.Receive(t, "the proxy's session DELETE to reach the daemon", entered)
			// The DELETE is held: the harness cleanup must be waiting on the proxy,
			// which is waiting on the daemon, before the daemon is let answer.
			early := false
			testwait.Until(t, "the harness cleanup to wait on the proxy's DELETE", func() (bool, string) {
				select {
				case <-returned:
					early = true
					return true, "cleanup returned"
				default:
				}
				proxyWaits, state := testwait.Blocked("mcpserv.(*stdioProxy).closeSession")
				if !proxyWaits {
					return false, "proxy: " + state
				}
				return testwait.Blocked("mcpserv.startProxyTestHarness.func2")
			})
			if early {
				close(release)
				t.Fatal("harness cleanup returned while the daemon session DELETE was still held")
			}
			if row.expire {
				fake.Advance(proxyCloseTimeout)
				testwait.Receive(t, "the proxy subtest to return once its close bound passed", returned)
				close(release)
			} else {
				close(release)
				testwait.Receive(t, "the proxy subtest to return", returned)
			}
			want := strings.ReplaceAll(row.wantWarning, "{url}", server.URL)
			if warned := warnings.String(); warned != want {
				t.Fatalf("proxy warnings while closing its daemon session = %q, want %q", warned, want)
			}
			if row.expire {
				return
			}
			select {
			case <-closed:
			default:
				t.Fatal("daemon session DELETE had not completed when harness cleanup returned")
			}
		})
	}
}
