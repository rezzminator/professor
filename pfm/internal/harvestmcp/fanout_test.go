package harvestmcp

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestFetchOneRecoversAPanicIntoThatItemsError is L2-F1's per-item fan-out
// half: a panic inside one source's read becomes that source's own error.
// service.harvester is nil here so the real read chain panics.
func TestFetchOneRecoversAPanicIntoThatItemsError(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 1)
	contents := make([]string, 1)
	items := make([]ReadItem, 1)
	wait.Add(1)
	service.fetchOne(
		context.Background(),
		semaphore,
		&wait,
		0,
		readJob{field: fieldURLs, source: "https://example.test/a"},
		readRequest{},
		contents,
		items,
	)
	wait.Wait()
	if !strings.Contains(items[0].Error, "harvester read item") {
		t.Fatalf("per-item error = %q, want it to name the fanout label", items[0].Error)
	}
	if !strings.Contains(contents[0], "ERROR") {
		t.Fatalf("contents[0] = %q, want an ERROR line", contents[0])
	}
	if len(semaphore) != 0 {
		t.Fatal("the semaphore slot was not released after the panic")
	}
}

// TestFetchOneMisplacedNeverReachesTheHarvester: a misplaced source answers
// its named error before the read (a nil harvester would panic otherwise).
func TestFetchOneMisplacedNeverReachesTheHarvester(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	contents := make([]string, 1)
	items := make([]ReadItem, 1)
	wait.Add(1)
	service.fetchOne(
		context.Background(),
		make(chan struct{}, 1),
		&wait,
		0,
		readJob{field: fieldURLs, source: "10.1038/nature14539"},
		readRequest{},
		contents,
		items,
	)
	wait.Wait()
	if !strings.Contains(items[0].Error, "put it in publications") || strings.Contains(items[0].Error, "read item") {
		t.Fatalf("misplaced item error = %q, want the publications pointer", items[0].Error)
	}
}
