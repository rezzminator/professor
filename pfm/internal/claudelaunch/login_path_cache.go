package claudelaunch

import (
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// loginPATHRetry is how long a failed probe's answer stands before the next caller probes again.
const loginPATHRetry = 60 * time.Second

// loginPATHCall is one probe of a login shell: done closes once result holds its
// answer, and retryAt, set before done closes, is when a failed answer goes stale
// (zero for a successful one, which stands for the life of the process).
type loginPATHCall struct {
	done    chan struct{}
	result  loginPATHResult
	retryAt time.Time
}

// stale reports whether call's probe has finished with a failure whose retry
// interval has run out by now; a probe still running is never stale.
func (call *loginPATHCall) stale(now time.Time) bool {
	select {
	case <-call.done:
		return !call.retryAt.IsZero() && !now.Before(call.retryAt)
	default:
		return false
	}
}

// loginPATHMemo holds each login shell's answer: a successful probe's for the
// life of the process, a failed one's (result.problem set) for loginPATHRetry.
// Its lock guards the map alone, never a probe: a caller asking for a shell
// whose probe is running waits for that probe's answer, and a caller asking
// for another shell is not held up by it.
type loginPATHMemo struct {
	mu      sync.Mutex
	byShell map[string]*loginPATHCall
}

// loginPATHCache is the process's memo, read by a launch with no runner of its own.
var loginPATHCache = &loginPATHMemo{byShell: map[string]*loginPATHCall{}}

// get is shell's answer: the one already given or being probed, else what
// probe returns, probe running once per shell however many callers ask at once;
// a failed answer stale on c is probed again by the first caller to find it so.
func (memo *loginPATHMemo) get(shell string, c clock.Clock, probe func() loginPATHResult) loginPATHResult {
	memo.mu.Lock()
	call, found := memo.byShell[shell]
	if found && call.stale(c.Now()) {
		found = false
	}
	if !found {
		call = &loginPATHCall{done: make(chan struct{})}
		memo.byShell[shell] = call
	}
	memo.mu.Unlock()
	if found {
		<-call.done
		return call.result
	}
	defer close(call.done)
	call.result = probe()
	if call.result.problem != "" {
		call.retryAt = c.Now().Add(loginPATHRetry)
	}
	return call.result
}
