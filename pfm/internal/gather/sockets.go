package gather

import (
	"os"
	"path/filepath"
	"strings"
)

// UnixSockets lists the bound path of every unix socket the kernel reports:
// {root}/net/unix for a jail root, else the kernel's own. macOS publishes no
// such table, so there it is errors.ErrUnsupported and the caller lists the
// socket directory it cares about instead.
func UnixSockets(root string) ([]string, error) {
	if jail := jailRoot(root); jail != "" {
		return readLinuxUnixSockets(jail)
	}
	return nativeUnixSockets()
}

// readLinuxUnixSockets reads net/unix: a header line, then one row per socket
// whose eighth field, when present, is its path.
func readLinuxUnixSockets(root string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(root, "net", "unix"))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(body), "\n")
	sockets := make([]string, 0, len(lines))
	for _, line := range lines[1:] {
		if fields := strings.Fields(line); len(fields) >= 8 {
			sockets = append(sockets, fields[len(fields)-1])
		}
	}
	return sockets, nil
}
