package fleetdb

import (
	"context"
	"reflect"
	"testing"
)

func TestLaunchMemoryRoundTrip(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	if got, err := state.LaunchMemory(ctx); err != nil || !reflect.DeepEqual(got, LaunchMemory{}) {
		t.Fatalf("empty store LaunchMemory() = %+v, %v; want zero, nil", got, err)
	}
	on, off := true, false
	steps := []struct {
		name    string
		engine  string
		account int
		cache   *bool
		want    LaunchMemory
	}{
		{
			"a claude launch keeps its account and cache", "cc", 2, &on,
			LaunchMemory{Engine: "cc", Accounts: map[string]int{"cc": 2}, Cache1H: map[int]bool{2: true}},
		},
		{
			"a codex launch moves the engine and adds its account, no cache", "cx", 3, nil,
			LaunchMemory{Engine: "cx", Accounts: map[string]int{"cc": 2, "cx": 3}, Cache1H: map[int]bool{2: true}},
		},
		{
			"another claude account gets its own cache", "cc", 1, &off,
			LaunchMemory{
				Engine: "cc", Accounts: map[string]int{"cc": 1, "cx": 3},
				Cache1H: map[int]bool{1: false, 2: true},
			},
		},
	}
	for index, step := range steps {
		if err := state.RememberLaunch(ctx, step.engine, step.account, step.cache, int64(100+index)); err != nil {
			t.Fatalf("%s: RememberLaunch: %v", step.name, err)
		}
		got, err := state.LaunchMemory(ctx)
		if err != nil || !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: LaunchMemory() = %+v, %v; want %+v", step.name, got, err, step.want)
		}
	}
}

func TestLaunchMemoryFailuresAreErrors(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	if err := state.RememberLaunch(ctx, "", 1, nil, 1); err == nil {
		t.Fatal("RememberLaunch without an engine succeeded")
	}
	if err := state.SetMeta(ctx, LaunchMemoryKey, "{not json", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := state.LaunchMemory(ctx); err == nil {
		t.Fatal("a corrupt launch memory row read as an empty memory")
	}
	if err := state.RememberLaunch(ctx, "cc", 1, nil, 2); err != nil {
		t.Fatalf("RememberLaunch could not replace a corrupt row: %v", err)
	}
	if got, err := state.LaunchMemory(ctx); err != nil || got.Engine != "cc" || got.Accounts["cc"] != 1 {
		t.Fatalf("LaunchMemory() after replacing a corrupt row = %+v, %v", got, err)
	}
	degraded := &Store{degraded: context.Canceled}
	if _, err := degraded.LaunchMemory(ctx); err == nil {
		t.Fatal("a degraded store read as an empty memory")
	}
}
