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
	items := make([]PageItem, 1)
	wait.Add(1)
	service.fetchOne(
		context.Background(),
		semaphore,
		&wait,
		0,
		"https://example.test/a",
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

// TestFetchOneMisrouteNeverReachesTheHarvester: a wrong-tool source answers
// its named error before the read (a nil harvester would panic otherwise).
func TestFetchOneMisrouteNeverReachesTheHarvester(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	contents := make([]string, 1)
	items := make([]PageItem, 1)
	wait.Add(1)
	service.fetchOne(context.Background(), make(chan struct{}, 1), &wait, 0, "10.1038/nature14539",
		readRequest{misroute: pageMisroute}, contents, items)
	wait.Wait()
	if !strings.Contains(items[0].Error, "`readWork`") || strings.Contains(items[0].Error, "read item") {
		t.Fatalf("misrouted item error = %q, want the readWork pointer", items[0].Error)
	}
}
