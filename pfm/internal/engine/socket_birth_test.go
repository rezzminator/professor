package engine_test

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestSocketBirthParsesFreshSocketNames(t *testing.T) {
	tests := []struct {
		name   string
		socket string
		want   int64
	}{
		{name: "opencode", socket: "ox-1789813424-3207648-22387", want: 1789813424},
		{name: "claude", socket: "cc-1700000000-42-7", want: 1700000000},
		{name: "codex", socket: "cx-1700000001-42-7", want: 1700000001},
		{name: "unknown prefix", socket: "zz-1700000000-42-7"},
		{name: "wrong arity", socket: "ox-1700000000-42"},
		{name: "not a number", socket: "ox-later-42-7"},
		{name: "negative", socket: "ox--5-42-7"},
		{name: "empty", socket: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pfmengine.SocketBirth(test.socket); got != test.want {
				t.Fatalf("SocketBirth(%q) = %d, want %d", test.socket, got, test.want)
			}
		})
	}
}
