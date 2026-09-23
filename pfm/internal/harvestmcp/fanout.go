package harvestmcp

import (
	"context"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// fetchOne runs one read-tool source in its own goroutine (readMany's
// per-item fan-out): the wrong-tool check, the semaphore gate, cancellation,
// the read itself and — L2-F1's per-item isolation half — a recovered panic,
// folded into that ITEM's own error rather than the whole batch or,
// unrecovered, the whole process (the SDK's request goroutine has no recover
// of its own).
func (service *Service) fetchOne(
	ctx context.Context,
	semaphore chan struct{},
	wait *sync.WaitGroup,
	index int,
	source string,
	request readRequest,
	contents []string,
	items []PageItem,
) {
	defer wait.Done()
	fail := func(message string) {
		items[index] = PageItem{Source: source, Error: message}
		contents[index] = service.describeFetch(
			source, harvest.Result{Source: source, Error: message}, request.options.SizeOnly,
		)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			fail(obs.Recovered("harvester read item", recovered).Error())
		}
	}()
	if request.misroute != nil {
		if message := request.misroute(source); message != "" {
			items[index] = PageItem{Source: source, Error: message}
			contents[index] = "# " + harvest.PublicSourceLabel(source) + "\nERROR: " + message
			return
		}
	}
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		fail("read cancelled: " + ctx.Err().Error())
		return
	}
	defer func() { <-semaphore }()
	scope := service.harvester.ForCaller
	if request.work {
		scope = service.harvester.ForWork // an identifier's headers wait for its landing origin
	}
	harvester, scopedCtx, err := scope(ctx, request.headers, source)
	if err != nil {
		fail(err.Error()) // no request was sent: the headers have no origin to go to
		return
	}
	fetched := request.headers.MarkHeaderless(harvester.FetchPublic(scopedCtx, source, request.options))
	items[index] = service.pageItem(source, fetched, request.work)
	if service.runtime.Remote {
		fetched.Path = "" // no result of the remote server carries a server path
	}
	contents[index] = service.describeFetch(source, fetched, request.options.SizeOnly)
}
