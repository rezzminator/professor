package herdr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// resolveFake is a Resolver's seams over a fixed Herdr: the environ of each
// pid, the listed panes and each pane's foreground pids, recording the order
// the panes were asked about.
type resolveFake struct {
	environ    map[int]map[string]string
	environErr error
	panes      []Pane
	panesErr   error
	foreground map[string][]int
	failing    map[string]bool
	asked      []string
	listed     int
}

func (fake *resolveFake) resolver() Resolver {
	return Resolver{
		Environ: func(pid int) (map[string]string, error) {
			if fake.environErr != nil {
				return nil, fake.environErr
			}
			return fake.environ[pid], nil
		},
		Panes: func(_ context.Context, _ string) ([]Pane, error) {
			fake.listed++
			return fake.panes, fake.panesErr
		},
		Foreground: func(_ context.Context, _, pane string) ([]int, error) {
			fake.asked = append(fake.asked, pane)
			if fake.failing[pane] {
				return nil, errors.New("pane_not_found")
			}
			return fake.foreground[pane], nil
		},
	}
}

const resolveSocket = "/jail/herdr/herdr.sock"

func inHerdr(pane string) map[string]string {
	return map[string]string{EnvMarker: "1", EnvSocket: resolveSocket, EnvPane: pane}
}

var resolvePanes = []Pane{{PaneID: "w1:p2", TerminalID: "t2"}, {PaneID: "w1:p3", TerminalID: "t3"}}

func TestResolveFindsTheClientsOwnPane(t *testing.T) {
	fake := &resolveFake{
		environ:    map[int]map[string]string{4101: inHerdr("w1:p3")},
		panes:      resolvePanes,
		foreground: map[string][]int{"w1:p2": {4100}, "w1:p3": {4099, 4101}},
	}
	location, found, err := fake.resolver().Resolve(context.Background(), 4101)
	if err != nil || !found {
		t.Fatalf("Resolve = %+v, %v, %v; want found, no error", location, found, err)
	}
	want := Location{Socket: resolveSocket, Pane: "w1:p3", Terminal: "t3"}
	if location != want {
		t.Fatalf("Resolve = %+v, want %+v", location, want)
	}
	if strings.Join(fake.asked, ",") != "w1:p3" {
		t.Fatalf("panes asked = %v, want only the env pane w1:p3", fake.asked)
	}
}

func TestResolveFallsBackToAPaneWalk(t *testing.T) {
	fake := &resolveFake{
		// The env pane is the one the pane's shell started with: dead after a
		// Herdr restart, while the client now runs in w1:p3.
		environ:    map[int]map[string]string{4101: inHerdr("w9:p1")},
		panes:      resolvePanes,
		foreground: map[string][]int{"w1:p2": {4100}, "w1:p3": {4101}},
	}
	location, found, err := fake.resolver().Resolve(context.Background(), 4101)
	if err != nil || !found {
		t.Fatalf("Resolve = %+v, %v, %v; want found, no error", location, found, err)
	}
	if location.Pane != "w1:p3" || location.Terminal != "t3" {
		t.Fatalf("Resolve = %+v, want pane w1:p3 terminal t3", location)
	}
	if strings.Join(fake.asked, ",") != "w1:p2,w1:p3" {
		t.Fatalf("panes asked = %v, want the walk w1:p2,w1:p3", fake.asked)
	}
}

func TestResolveOutsideHerdrIsNoError(t *testing.T) {
	fake := &resolveFake{environ: map[int]map[string]string{4101: {"TERM": "xterm-256color"}}, panes: resolvePanes}
	location, found, err := fake.resolver().Resolve(context.Background(), 4101)
	if err != nil || found || location != (Location{}) {
		t.Fatalf("Resolve = %+v, %v, %v; want not found, no error", location, found, err)
	}
	if fake.listed != 0 || len(fake.asked) != 0 {
		t.Fatalf("a client outside Herdr asked Herdr: %d pane lists, %v process infos", fake.listed, fake.asked)
	}
}

func TestResolveNamesAClientInNoPane(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *resolveFake
		want string
	}{
		{
			name: "no pane holds the pid",
			fake: &resolveFake{
				environ:    map[int]map[string]string{4101: inHerdr("w1:p3")},
				panes:      resolvePanes,
				foreground: map[string][]int{"w1:p2": {4100}, "w1:p3": {4099}},
			},
			want: "client 4101 runs in no pane of " + resolveSocket +
				"; nested tmux is unsupported: Herdr exposes only the outer foreground process group; " +
				"attach the pfm client directly in Herdr",
		},
		{
			name: "nested tmux with one outer attachment",
			fake: &resolveFake{
				environ:    map[int]map[string]string{4101: inHerdr("w1:p2")},
				panes:      resolvePanes,
				foreground: map[string][]int{"w1:p2": {7101}, "w1:p3": {7201}},
			},
			want: "client 4101 runs in no pane of " + resolveSocket +
				"; nested tmux is unsupported: Herdr exposes only the outer foreground process group; " +
				"attach the pfm client directly in Herdr",
		},
		{
			name: "nested tmux with two outer attachments",
			fake: &resolveFake{
				environ:    map[int]map[string]string{4101: inHerdr("w1:p2")},
				panes:      resolvePanes,
				foreground: map[string][]int{"w1:p2": {7101}, "w1:p3": {7102}},
			},
			want: "client 4101 runs in no pane of " + resolveSocket +
				"; nested tmux is unsupported: Herdr exposes only the outer foreground process group; " +
				"attach the pfm client directly in Herdr",
		},
		{
			name: "marker without a socket",
			fake: &resolveFake{environ: map[int]map[string]string{4101: {EnvMarker: "1", EnvPane: "w1:p3"}}},
			want: "client 4101: HERDR_ENV=1 without HERDR_SOCKET_PATH",
		},
		{
			name: "environ unreadable",
			fake: &resolveFake{environErr: errors.New("no such process")},
			want: "read the environment of client 4101: no such process",
		},
		{
			name: "pane list fails",
			fake: &resolveFake{
				environ:  map[int]map[string]string{4101: inHerdr("w1:p3")},
				panesErr: errors.New("connection refused"),
			},
			want: "list the panes of " + resolveSocket + ": connection refused",
		},
		{
			name: "every process info fails",
			fake: &resolveFake{
				environ: map[int]map[string]string{4101: inHerdr("w1:p3")},
				panes:   resolvePanes,
				failing: map[string]bool{"w1:p2": true, "w1:p3": true},
			},
			want: "client 4101 runs in no pane of " + resolveSocket +
				" (process info failed for 2 of 2 panes: w1:p3: pane_not_found)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, found, err := tc.fake.resolver().Resolve(context.Background(), 4101)
			if err == nil || found || location != (Location{}) {
				t.Fatalf("Resolve = %+v, %v, %v; want the error %q", location, found, err, tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("Resolve error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestPaneRunningSkipsAPaneWhoseProcessInfoFails(t *testing.T) {
	fake := &resolveFake{
		panes:      resolvePanes,
		foreground: map[string][]int{"w1:p3": {4101}},
		failing:    map[string]bool{"w1:p2": true},
	}
	pane, found, err := fake.resolver().PaneRunning(context.Background(), resolveSocket, 4101, resolvePanes, "")
	if err != nil || !found || pane.PaneID != "w1:p3" {
		t.Fatalf("PaneRunning = %+v, %v, %v; want w1:p3 found, no error", pane, found, err)
	}
}

func TestWireResolverReadsTheHerdrSocket(t *testing.T) {
	fake := serveHerdr(t, map[string]string{
		"pane.list": `{"type":"pane_list","panes":[` +
			`{"pane_id":"w1:p2","terminal_id":"t2"},{"pane_id":"w1:p3","terminal_id":"t3"}]}`,
		"pane.process_info": `{"type":"pane_process_info","process_info":` +
			`{"foreground_processes":[{"pid":4101}]}}`,
	})
	var seq uint64
	resolver := WireResolver(
		func(int) (map[string]string, error) {
			return map[string]string{EnvMarker: "1", EnvSocket: fake.path, EnvPane: "w1:p3"}, nil
		},
		Call,
		func() uint64 { seq++; return seq },
	)
	location, found, err := resolver.Resolve(context.Background(), 4101)
	if err != nil || !found {
		t.Fatalf("Resolve = %+v, %v, %v; want found", location, found, err)
	}
	if location != (Location{Socket: fake.path, Pane: "w1:p3", Terminal: "t3"}) {
		t.Fatalf("Resolve = %+v, want w1:p3/t3 on %s", location, fake.path)
	}
	if fake.count("pane.list") != 1 || fake.count("pane.process_info") != 1 {
		t.Fatalf("requests: %d pane.list, %d process_info; want 1 each",
			fake.count("pane.list"), fake.count("pane.process_info"))
	}
}
