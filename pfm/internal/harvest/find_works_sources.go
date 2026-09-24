package harvest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The status a discovery source ends a FindWorks call with. A source that
// failed is named apart from one that answered with nothing: an outage is
// never read as an empty shelf.
const (
	SourceAnswered = "answered"
	SourcePartial  = "partial"
	SourceFailed   = "failed"
	// SourceTimedOut: the source was still running when the FindWorks
	// deadline came; it was cancelled, and its Error names the time it had.
	SourceTimedOut = "timed_out"
)

// WorkSource is one discovery source's answer to a FindWorks call: its public
// name, its status, how many records it returned, and, when a request to it
// failed, what failed.
type WorkSource struct {
	Name    string
	Status  string
	Results int
	Error   string
}

// WorksFound is FindWorks' answer with every source's status beside the
// ranked candidates.
type WorksFound struct {
	Candidates []Candidate
	Sources    []WorkSource
}

// Failed lists the sources that did not answer in full.
func (found WorksFound) Failed() []WorkSource {
	failed := []WorkSource{}
	for _, source := range found.Sources {
		if source.Status != SourceAnswered {
			failed = append(failed, source)
		}
	}
	return failed
}

// FailedText names each failed source and what failed, for an error or a
// rendering: "Semantic Scholar (HTTP 429 Too Many Requests); arXiv (timed out)".
func FailedText(sources []WorkSource) string {
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, source.Name+" ("+source.Error+")")
	}
	return strings.Join(parts, "; ")
}

// sourceProbe watches one discovery source's requests: the gather functions
// answer nil on any failure, so the probe is what tells a failed request from
// an empty answer. A later success on the same host clears its failure.
type sourceProbe struct {
	base     http.RoundTripper
	mu       sync.Mutex
	failures map[string]string
}

func newSourceProbe(client *http.Client) (*sourceProbe, *http.Client) {
	probe := &sourceProbe{base: client.Transport, failures: map[string]string{}}
	if probe.base == nil {
		probe.base = http.DefaultTransport
	}
	clone := *client
	clone.Transport = probe
	return probe, &clone
}

func (probe *sourceProbe) note(host, failure string) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if failure == "" {
		delete(probe.failures, host)
		return
	}
	probe.failures[host] = failure
}

func (probe *sourceProbe) RoundTrip(request *http.Request) (*http.Response, error) {
	host := request.URL.Hostname()
	response, err := probe.base.RoundTrip(request)
	switch {
	case err != nil:
		probe.note(host, requestFailure(err))
	case response.StatusCode >= 400:
		probe.note(host, fmt.Sprintf("HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode)))
	default:
		probe.note(host, "")
		response.Body = &probedBody{ReadCloser: response.Body, probe: probe, host: host}
	}
	return response, err
}

// failure is "" when every host this source asked answered, else each failed
// host's failure, the host named when the source asks more than one.
func (probe *sourceProbe) failure(hosts int) string {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	parts := make([]string, 0, len(probe.failures))
	for host, failure := range probe.failures {
		if hosts > 1 {
			failure = host + ": " + failure
		}
		parts = append(parts, failure)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// probedBody records a body cut off mid-read (a deadline while the answer
// streams) as the host's failure.
type probedBody struct {
	io.ReadCloser
	probe *sourceProbe
	host  string
}

func (body *probedBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		body.probe.note(body.host, requestFailure(err))
	}
	return count, err
}

func requestFailure(err error) string {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "request failed: " + redactFailureText(err.Error())
}

// sourceStatus is a source's WorkSource from its answer and its failure.
func sourceStatus(name string, results int, failure string) WorkSource {
	status := SourceAnswered
	switch {
	case failure != "" && results > 0:
		status = SourcePartial
	case failure != "":
		status = SourceFailed
	}
	return WorkSource{Name: name, Status: status, Results: results, Error: failure}
}

// errNoSourceAnswered is FindWorksReport's error when every source failed.
func errNoSourceAnswered(found WorksFound) error {
	return fmt.Errorf("every discovery source failed: %s", FailedText(found.Failed()))
}

// workSearch is one discovery source of a FindWorks call: its public name,
// whether the call waits for it before its grace (core), and a search that
// answers its candidates and, when a request failed, what failed.
type workSearch struct {
	name string
	core bool
	run  func(context.Context) ([]Candidate, string)
}

// gatherWorks runs every search at once and waits until each answered or ctx
// ended. Once every core search finished and the answers hold at least
// minCandidates distinct records, it waits grace for the rest. A search still
// running at the end is named timed_out — with the grace's text, the deadline
// it was given, or the caller's cancellation — never dropped; it is left to see
// ctx's cancellation and exit on its own.
func gatherWorks(
	ctx context.Context,
	searches []workSearch,
	deadline, grace time.Duration,
	minCandidates int,
) ([][]Candidate, []WorkSource) {
	type answer struct {
		index      int
		candidates []Candidate
		source     WorkSource
	}
	answers := make(chan answer, len(searches)) // a late answer never blocks its goroutine
	coreLeft, hasCore := 0, false
	for index, search := range searches {
		if search.core {
			coreLeft, hasCore = coreLeft+1, true
		}
		go func() {
			// A recovered panic (F1) fails its own source by name: one bad
			// provider response must not take the other sources, let alone
			// the daemon calling FindWorks, down with it.
			defer recoverItem(func(e error) {
				answers <- answer{index, nil, sourceStatus(
					search.name, 0, "its answer could not be processed: "+redactFailureText(e.Error()),
				)}
			})
			candidates, failure := search.run(ctx)
			answers <- answer{index, candidates, sourceStatus(search.name, len(candidates), failure)}
		}()
	}
	parts := make([][]Candidate, len(searches))
	sources := make([]WorkSource, len(searches))
	answered := make([]bool, len(searches))
	timeOut := func(failure string) ([][]Candidate, []WorkSource) {
		for index, search := range searches {
			if !answered[index] {
				sources[index] = WorkSource{Name: search.name, Status: SourceTimedOut, Error: failure}
			}
		}
		return parts, sources
	}
	distinct := map[string]bool{}
	var settled <-chan struct{} // the grace, armed once the core finished
	graceScope, endGrace := context.WithCancel(context.Background())
	defer endGrace()
	for range searches {
		select {
		case got := <-answers:
			parts[got.index], sources[got.index], answered[got.index] = got.candidates, got.source, true
			if searches[got.index].core {
				coreLeft--
			}
			for _, candidate := range got.candidates {
				if candidate.URL != "" {
					distinct[candidate.URL] = true
				}
			}
			if hasCore && settled == nil && coreLeft == 0 && len(distinct) >= minCandidates {
				settled = graceAfter(graceScope, grace)
			}
		case <-settled:
			return timeOut(findWorksNotWaitedText)
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return timeOut("cancelled by the caller before it answered")
			}
			return timeOut(fmt.Sprintf("no answer within the %s findWorks deadline; cancelled", deadline))
		}
	}
	return parts, sources
}

// graceAfter is a channel closed grace from now, or when scope ends; scope's
// end also releases the grace's timer.
func graceAfter(scope context.Context, grace time.Duration) <-chan struct{} {
	ctx, cancel := context.WithTimeout(scope, grace)
	context.AfterFunc(scope, cancel)
	return ctx.Done()
}
