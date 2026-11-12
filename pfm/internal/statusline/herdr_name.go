package statusline

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RenameHerdrSeat names the native Herdr seat of a Claude session after the
// title its statusline carries: chat.ConvergeHerdrSessionName, registered by
// package chat (statusline cannot import chat, which reaches statusline through
// stats). Nil, as in this package's own tests, leaves the name to the hooks and
// the name-sync backstop.
var RenameHerdrSeat func(ctx context.Context, values paths.Values, sessionID, title string) error

// herdrConvergeTimeout bounds one seat rename: a lock, a supervisor proof and
// two Herdr requests, inside the paced worker the chat's front never waits on.
const herdrConvergeTimeout = 2 * time.Second

// convergeChatName carries a Claude /rename to the chat's terminal the moment
// its statusline renders it: the tmux window, or the native Herdr seat.
func convergeChatName(ctx context.Context, runtime Runtime, data input) {
	convergeWindowName(ctx, runtime, data)
	convergeHerdrName(ctx, runtime, data)
}

// convergeHerdrName is convergeWindowName's Herdr half. Claude fires no hook
// on /rename, so without it a native seat and its tab keep the old name until
// the chat's next turn. A converged chat reads one cache file and stops; the
// cache holds the Herdr socket and pane beside the name, so a resumed chat in
// another pane converges again, and is written only once the rename succeeded.
func convergeHerdrName(ctx context.Context, runtime Runtime, data input) {
	if RenameHerdrSeat == nil || runtime.Engine != pfmengine.Claude || data.Agent.Name != "" ||
		data.SessionID == "" || data.SessionName == "" {
		return
	}
	identity := runtime.identity()
	if identity.HerdrSocket == "" || identity.HerdrPane == "" {
		return
	}
	cachePath := filepath.Join(runtime.CacheDir, "cc-sl-herdr-name-"+data.SessionID)
	stamp := identity.HerdrSocket + "\n" + identity.HerdrPane + "\n" + data.SessionName
	if cached, err := os.ReadFile(cachePath); err == nil && string(cached) == stamp {
		return
	}
	renameContext, cancel := context.WithTimeout(ctx, herdrConvergeTimeout)
	defer cancel()
	values := paths.Values{StateDB: runtime.stateDB(), ProcRoot: runtime.ProcRoot}
	if err := RenameHerdrSeat(renameContext, values, data.SessionID, data.SessionName); err != nil {
		fmt.Fprintf(
			runtime.Warn,
			"statusline: name the Herdr seat of %s %q: %v\n",
			data.SessionID,
			data.SessionName,
			err,
		)
		return
	}
	if err := atomicfile.Write(cachePath, []byte(stamp), 0o600); err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: record the Herdr seat name of %s: %v\n", data.SessionID, err)
	}
}

// stateDB is the fleet database a render reads: PFM_STATE_DB, else the one the
// machine config resolved, else the default under Home.
func (runtime Runtime) stateDB() string {
	return cmp.Or(runtime.getenv(paths.EnvStateDB), runtime.StateDB, paths.DefaultStateDB(runtime.Home))
}
