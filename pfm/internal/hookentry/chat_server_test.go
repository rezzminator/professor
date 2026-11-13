package hookentry

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"
)

// A cx placed in Herdr is born through chat new carrying its one typed prompt;
// a shell holding an older shim passes the prompt only inside its run string,
// from which it is carried, said, never born without it.
func TestChatServerNativeBirthCarriesTheTypedPrompt(t *testing.T) {
	const socket = "cx-1800000055-1-1"
	run := `"/h/.local/bin/pfm" internal codex-launch`
	born := []string{"new", "--engine=cx", "--name", socket, "--cwd", "/tmp", "--attach", "--"}
	const olderShim = "pfm internal chat-server: this shell's cx predates this pfm and passed its words only " +
		"inside its run string; carried from there — open a new shell (or run: source ~/.zshrc)\n"
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		birth  []string
		stderr string
	}{
		{name: "no prompt", args: []string{socket, "/tmp", run + " "}, code: ChatServerBornNative, birth: born},
		{
			name: "typed prompt", args: []string{socket, "/tmp", run + " fix\\ it", "fix it"},
			code: ChatServerBornNative, birth: append(slices.Clone(born), "fix it"),
		},
		{
			name: "older shim's prompt", args: []string{socket, "/tmp", run + " fix\\ it"},
			code: ChatServerBornNative, birth: append(slices.Clone(born), "fix it"), stderr: olderShim,
		},
		{
			name: "older shim's three arguments", args: []string{socket, "/tmp", run + " --resume abc"}, code: 1,
			stderr: olderShim + "pfm internal chat-server: a cx born in Herdr carries one prompt only, not " +
				`"--resume abc"; type ` + "`codex …`" + " in this pane, or start it with: " +
				"pfm chat new {name} --engine codex\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			var birth []string
			code := chatServerNative(context.Background(), tc.args, io.Discard, &stderr,
				func(_ context.Context, args []string, _, _ io.Writer) int {
					birth = args
					return 0
				})
			if code != tc.code || !slices.Equal(birth, tc.birth) || stderr.String() != tc.stderr {
				t.Fatalf("code=%d birth=%q stderr=%q\nwant code=%d birth=%q stderr=%q",
					code, birth, stderr.String(), tc.code, tc.birth, tc.stderr)
			}
		})
	}
}
