package mcpserv

import (
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func (current *backend) rowSocketPath(row ChatRow) (string, error) {
	if row.Backend == spawn.BackendHerdr {
		if !filepath.IsAbs(row.Socket) {
			return "", fmt.Errorf("native Herdr socket is not absolute: %q", row.Socket)
		}
		return row.Socket, nil
	}
	return current.paths.SocketUnder(row.Socket)
}
