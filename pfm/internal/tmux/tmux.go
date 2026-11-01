// Package tmux is the one tmux runner: every tmux invocation pfm makes on a
// chat's server is built by Command, so the socket addressing, the binary
// lookup and the cleared $TMUX live in one place. Each consumer package keeps
// its own narrow interface over it (its fake is its test seam).
package tmux

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Binary is the executable name shared by every tmux caller.
const Binary = "tmux"

// valueFlags are tmux's global flags that take a value (its getopt string
// "2c:CDdf:lL:NqS:T:uUvV").
const valueFlags = "cfLST"

// SocketPath returns the server socket component of a TMUX value.
func SocketPath(value string) string {
	socket, _, _ := strings.Cut(value, ",")
	return socket
}

// NamedServer reads the server a tmux command line names, as tmux's own
// option parser takes it: the -L label and the -S socket path, each "" when
// absent (-S wins over -L in tmux). isTmux is false when argv[0] is not tmux.
// A tmux server keeps the command line of the client that started it, so this
// reads a server's argv as well as a client's.
func NamedServer(argv []string) (label, socketPath string, isTmux bool) {
	if len(argv) == 0 || filepath.Base(argv[0]) != Binary {
		return "", "", false
	}
	for index := 1; index < len(argv); index++ {
		argument := argv[index]
		if argument == "--" || !strings.HasPrefix(argument, "-") || argument == "-" {
			break
		}
		// Flags group as getopt(3) groups them ("-uL work", "-2S/path"): each
		// letter is a flag until one taking a value, which takes the rest of
		// the token or, when none is left, the next argument.
		for position := 1; position < len(argument); position++ {
			flag := argument[position]
			if !strings.ContainsRune(valueFlags, rune(flag)) {
				continue
			}
			value := argument[position+1:]
			if value == "" && index+1 < len(argv) {
				index++
				value = argv[index]
			}
			switch flag {
			case 'L':
				label = value
			case 'S':
				socketPath = value
			}
			break
		}
	}
	return label, socketPath, true
}

// CouldNotRun reports whether err means tmux itself never started — the
// binary is absent from PATH, a configured path does not exist, or it is not
// executable — as opposed to a tmux that ran and failed against one server.
// A probe sweeping every socket must fail whole on this class: no socket
// could be read, so an empty result would claim "no chats" when the truth is
// "could not look". A service manager's bare PATH (launchd's
// /usr/bin:/bin:/usr/sbin:/sbin) is the live way to get here.
func CouldNotRun(err error) bool {
	var lookup *exec.Error
	if errors.As(err, &lookup) {
		return true
	}
	var start *fs.PathError
	return errors.As(err, &start) && strings.HasPrefix(start.Op, "fork/exec")
}

// Command is one tmux invocation on the server at socketPath (tmux -S).
// binary "" means the registered tmux (deps.Executable); a configured binary
// runs exactly as given. TMUX is set, empty: a command run from inside a chat must
// never nest into the caller's own server, and a defined $TMUX is also what
// makes tmux return control characters in a format string verbatim rather
// than as "_".
func Command(ctx context.Context, binary, socketPath string, arguments ...string) *exec.Cmd {
	path, argv, environment := Invocation(binary, socketPath, arguments...)
	command := exec.CommandContext(ctx, path, argv...)
	command.Env = environment
	return command
}

// Invocation is Command unassembled — the resolved binary, its arguments and
// its environment — for a caller that runs tmux under another launcher
// (spawn's durable systemd scope).
func Invocation(binary, socketPath string, arguments ...string) (string, []string, []string) {
	if binary == "" {
		binary = deps.Executable(Binary)
	}
	return binary, append([]string{"-S", socketPath}, arguments...), Environment()
}

// Environment is the environment every tmux run gets: the caller's own, with
// TMUX set and empty (see Command). A caller replacing pfm with tmux
// (syscall.Exec of a generated action line) passes it unchanged.
func Environment() []string {
	return deps.EnvironmentWith("TMUX", "")
}
