package engine

import "testing"

func TestSocketKeyedIDNamesOnlyAnAddressAsAnIdentity(t *testing.T) {
	const socket = "ox-1700000000-42-7"
	tests := []struct {
		name   string
		engine ID
		id     string
		socket string
		want   bool
	}{
		{name: "unidentified OpenCode seat", engine: OpenCode, id: socket, socket: socket, want: true},
		{name: "identified OpenCode seat", engine: OpenCode, id: "ses_live", socket: socket, want: false},
		{name: "resumable OpenCode row", engine: OpenCode, id: "ses_cold", socket: "", want: false},
		{name: "no id and no socket", engine: OpenCode, id: "", socket: "", want: false},
		{name: "Claude seat", engine: Claude, id: "cc-1-2-3", socket: "cc-1-2-3", want: false},
		{name: "Codex seat", engine: Codex, id: "cx-1-2-3", socket: "cx-1-2-3", want: false},
	}
	for _, test := range tests {
		if got := SocketKeyedID(test.engine, test.id, test.socket); got != test.want {
			t.Fatalf("%s: SocketKeyedID(%q, %q, %q) = %v, want %v",
				test.name, test.engine, test.id, test.socket, got, test.want)
		}
	}
}
