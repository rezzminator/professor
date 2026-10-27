package herdr

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// IndicatorsEntry is `pfm internal herdr-indicators`, the command pfm's Herdr
// indicator plugin execs on each hook: it paints the sidebar indicators of the
// Herdr server the hook names in HERDR_SOCKET_PATH (Herdr sets it for every
// plugin command) and, while an agent is blocked, keeps the one blinker alive
// until nothing is or a signal ends it. paint is PaintIndicators, a seam for
// the entry's test; it writes its own errors to stderr (Herdr's plugin log). A
// failure is exit 1.
func IndicatorsEntry(
	stderr io.Writer,
	env paths.Env,
	paint func(ctx context.Context, socket, lockDir string, stderr io.Writer) error,
) int {
	home, err := env.Home()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal %s: resolve home directory: %v\n", IndicatorVerb, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := paint(ctx, env.Get(EnvSocket), paths.HerdrLockDir(home), stderr); err != nil {
		return 1
	}
	return 0
}
