package claudelaunch

import "sync"

// loginPATHCall is one probe of a login shell: done closes once result holds its answer.
type loginPATHCall struct {
	done   chan struct{}
	result loginPATHResult
}

// loginPATHMemo holds each login shell's answer for the life of the process.
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
// probe returns, probe running once per shell however many callers ask at once.
func (memo *loginPATHMemo) get(shell string, probe func() loginPATHResult) loginPATHResult {
	memo.mu.Lock()
	call, found := memo.byShell[shell]
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
	return call.result
}
