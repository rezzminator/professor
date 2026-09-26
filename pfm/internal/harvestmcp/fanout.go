package harvestmcp

import (
	"context"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// fetchOne runs one read item in its own goroutine (readMany's per-item
// fan-out): the misplaced-field check, the semaphore gate, cancellation, the
// read itself and — L2-F1's per-item isolation half — a recovered panic,
// folded into that ITEM's own error rather than the whole batch or,
// unrecovered, the whole process (the SDK's request goroutine has no recover
// of its own).
func (service *Service) fetchOne(
	ctx context.Context,
	semaphore chan struct{},
	wait *sync.WaitGroup,
	index int,
	job readJob,
	request readRequest,
	contents []string,
	items []ReadItem,
) {
	defer wait.Done()
	source := job.source
	fail := func(message string) {
		items[index] = ReadItem{Source: source, Gaps: []string{}, Error: message}
		contents[index] = service.describeFetch(
			source, harvest.Result{Source: source, Error: message}, request.options.SizeOnly,
		)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			fail(obs.Recovered("harvester read item", recovered).Error())
		}
	}()
	if message := misplaced(job.field, source, service.runtime.Remote); message != "" {
		items[index] = ReadItem{Source: source, Gaps: []string{}, Error: message}
		contents[index] = "# " + harvest.PublicSourceLabel(source) + "\nERROR: " + message
		return
	}
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		fail("read cancelled: " + ctx.Err().Error())
		return
	}
	defer func() { <-semaphore }()
	scope := service.harvester.ForCaller
	headers := request.headers
	switch job.field {
	case fieldPublications:
		scope = service.harvester.ForWork // an identifier's headers wait for its landing origin
	case fieldFiles:
		headers = harvest.CallerHeaders{} // a caller's headers never go with a local file
	}
	harvester, scopedCtx, err := scope(ctx, headers, source)
	if err != nil {
		fail(err.Error()) // no request was sent: the headers have no origin to go to
		return
	}
	fetched := headers.MarkHeaderless(harvester.FetchPublic(scopedCtx, source, request.options))
	items[index] = service.readItem(job, fetched, !request.options.SizeOnly)
	if service.runtime.Remote {
		fetched.Path = "" // no result of the remote server carries a server path
	}
	contents[index] = service.describeFetch(source, fetched, request.options.SizeOnly)
}
