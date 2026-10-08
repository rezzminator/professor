package harvestmcp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestFetchOneRecoversAPanicIntoThatItemsError is L2-F1's per-item fan-out
// half: a panic inside one source's read becomes that source's own error.
// service.harvester is nil here so the real read chain panics.
func TestFetchOneRecoversAPanicIntoThatItemsError(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 1)
	results := make([]harvest.Result, 1)
	wait.Add(1)
	service.fetchOne(
		context.Background(),
		semaphore,
		&wait,
		0,
		readJob{field: fieldURLs, source: "https://example.test/a"},
		readRequest{},
		results,
	)
	wait.Wait()
	if !strings.Contains(results[0].Error, "harvester read item") {
		t.Fatalf("per-item error = %q, want it to name the fanout label", results[0].Error)
	}
	if block, failed := RenderReadItem(1, 1, "https://example.test/a", results[0], ReadView{}); !failed ||
		!strings.Contains(block, "\nerror: ") {
		t.Fatalf("block = %q, want an error line", block)
	}
	if len(semaphore) != 0 {
		t.Fatal("the semaphore slot was not released after the panic")
	}
}

// TestReadManyMisplacedNeverReachesTheHarvester: a misplaced source answers
// its named error before the read (a nil harvester would panic otherwise).
func TestReadManyMisplacedNeverReachesTheHarvester(t *testing.T) {
	result := (&Service{}).readMany(context.Background(),
		[]readJob{{field: fieldURLs, source: "10.1038/nature14539"}}, readRequest{})
	text, _ := result.Content[0].(*mcp.TextContent)
	if !result.IsError || text == nil ||
		text.Text != "=== [1/1] 10.1038/nature14539\nerror: this is a DOI; put it in publications." {
		t.Fatalf("misplaced item answer = %+v, want its own publications pointer", result.Content)
	}
}
