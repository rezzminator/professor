package harvestmcp

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestFetchOneRecoversAPanicIntoThatItemsError is L2-F1's per-item fan-out
// half (service.go's old :819, now fetchOne): a panic inside one source's
// fetch becomes that source's own error, never an unrecovered panic that
// would (on the SDK's un-recovering request goroutine) kill the whole
// process. service.harvester is nil here so the real fetch chain panics on
// a field access deep inside FetchWithOptions — a stand-in for any other
// panicking input.
func TestFetchOneRecoversAPanicIntoThatItemsError(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 1)
	contents := make([]string, 1)
	items := make([]FetchItem, 1)
	wait.Add(1)
	service.fetchOne(context.Background(), semaphore, &wait, 0, "https://example.test/a", FetchInput{}, contents, items)
	wait.Wait()
	if items[0].Error == "" {
		t.Fatalf("a panicking fetch did not record a per-item error: %+v", items[0])
	}
	if !strings.Contains(items[0].Error, "harvester fetch item") {
		t.Fatalf("per-item error = %q, want it to name the fanout label", items[0].Error)
	}
	if !strings.Contains(contents[0], "ERROR") {
		t.Fatalf("contents[0] = %q, want an ERROR line", contents[0])
	}
	if len(semaphore) != 0 {
		t.Fatal("the semaphore slot was not released after the panic")
	}
}

// TestImageOneRecoversAPanicIntoThatItemsError is imageOne's sibling test
// (service.go's old :930).
func TestImageOneRecoversAPanicIntoThatItemsError(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 1)
	contents := make([]string, 1)
	items := make([]ImageItem, 1)
	wait.Add(1)
	service.imageOne(context.Background(), semaphore, &wait, 0, "https://example.test/a.png", items, contents)
	wait.Wait()
	if items[0].Error == "" {
		t.Fatalf("a panicking fetchImage did not record a per-item error: %+v", items[0])
	}
	if !strings.Contains(items[0].Error, "harvester fetchImage item") {
		t.Fatalf("per-item error = %q, want it to name the fanout label", items[0].Error)
	}
	if !strings.Contains(contents[0], "ERROR") {
		t.Fatalf("contents[0] = %q, want an ERROR line", contents[0])
	}
	if len(semaphore) != 0 {
		t.Fatal("the semaphore slot was not released after the panic")
	}
}

// TestFetchOnePassesThroughSourcesThatDoNotPanic proves the isolation cuts
// both ways: a fan-out over several sources where only one panics leaves the
// others' results untouched — the negative control for the two tests above.
func TestFetchOnePassesThroughSourcesThatDoNotPanic(t *testing.T) {
	service := &Service{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 2)
	sources := []string{"https://example.test/ok", "https://example.test/panics"}
	contents := make([]string, len(sources))
	items := make([]FetchItem, len(sources))
	for index, source := range sources {
		wait.Add(1)
		go service.fetchOne(context.Background(), semaphore, &wait, index, source, FetchInput{}, contents, items)
	}
	wait.Wait()
	for index := range sources {
		if items[index].Error == "" {
			t.Fatalf("item %d has no error (nil harvester should fail every source): %+v", index, items[index])
		}
	}
}
