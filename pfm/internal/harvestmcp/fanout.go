package harvestmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"hostops/pfm/internal/harvest"
	"hostops/pfm/internal/obs"
)

// fetchOne runs one fetch-tool source in its own goroutine (service.fetch's
// per-item fan-out): the semaphore gate, cancellation, the fetch itself and
// — L2-F1's per-item isolation half — a recovered panic, folded into that
// ITEM's own error rather than the whole batch or, unrecovered, the whole
// process (the SDK's request goroutine has no recover of its own).
func (service *Service) fetchOne(
	ctx context.Context,
	semaphore chan struct{},
	wait *sync.WaitGroup,
	index int,
	source string,
	input FetchInput,
	contents []string,
	items []FetchItem,
) {
	defer wait.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			err := obs.Recovered("harvester fetch item", recovered)
			items[index] = FetchItem{Source: source, Error: err.Error()}
			contents[index] = service.describeFetch(
				source, harvest.Result{Source: source, Error: err.Error()}, input.SizeOnly,
			)
		}
	}()
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		contents[index] = service.describeFetch(
			source, harvest.Result{Source: source, Error: "fetch cancelled: " + ctx.Err().Error()}, input.SizeOnly,
		)
		items[index] = FetchItem{Source: source, Error: "fetch cancelled: " + ctx.Err().Error()}
		return
	}
	defer func() { <-semaphore }()
	fetched := service.harvester.FetchPublic(
		ctx, source, harvest.FetchOptions{Refresh: input.Refresh, SizeOnly: input.SizeOnly},
	)
	items[index] = fetchItem(fetched)
	contents[index] = service.describeFetch(source, fetched, input.SizeOnly)
}

// imageOne is fetchOne's fetchImage-tool sibling — named apart from the
// existing fetchOneImage (the per-source harvester call it wraps below) so
// the two are never mistaken for each other.
func (service *Service) imageOne(
	ctx context.Context,
	semaphore chan struct{},
	wait *sync.WaitGroup,
	index int,
	source string,
	items []ImageItem,
	contents []string,
) {
	defer wait.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			err := obs.Recovered("harvester fetchImage item", recovered)
			items[index] = ImageItem{Source: source, Error: err.Error()}
			contents[index] = fmt.Sprintf("# %s\nERROR: %s", source, err.Error())
		}
	}()
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		items[index] = ImageItem{Source: source, Error: "fetch image cancelled: " + ctx.Err().Error()}
		contents[index] = fmt.Sprintf("# %s\nERROR: %s", source, items[index].Error)
		return
	}
	defer func() { <-semaphore }()
	item := service.fetchOneImage(ctx, source)
	items[index] = item
	body, err := json.Marshal(item)
	if err != nil {
		contents[index] = fmt.Sprintf("image receipt encoding failed for %q: %v", source, err)
		return
	}
	contents[index] = string(body)
}
