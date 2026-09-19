package action

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"hostops/pfm/internal/obs"
)

// ReaderGate is the injectable open-gate core. Reader and Writer must be the
// same terminal in production.
type ReaderGate struct {
	Reader io.Reader
	Writer io.Writer
}

func (gate ReaderGate) Confirm(
	ctx context.Context,
	request GateRequest,
) (bool, error) {
	accountMismatch := request.BirthAccount != 0 &&
		request.BirthAccount != request.PrimaryAccount
	cacheMismatch := request.BirthCache1H != request.WantCache1H
	if !accountMismatch && !cacheMismatch {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if gate.Reader == nil || gate.Writer == nil {
		return false, errors.New("open gate requires a reader and writer")
	}
	fmt.Fprintln(gate.Writer)
	fmt.Fprintf(
		gate.Writer,
		"⚠ birth env ≠ picker — %s is live\n",
		request.Name,
	)
	if accountMismatch {
		fmt.Fprintf(
			gate.Writer,
			"  account  %d → %d\n",
			request.BirthAccount,
			request.PrimaryAccount,
		)
	}
	if cacheMismatch {
		fmt.Fprintf(
			gate.Writer,
			"  cache    %s → %s\n",
			cacheName(request.BirthCache1H),
			cacheName(request.WantCache1H),
		)
	}
	fmt.Fprintln(gate.Writer, "Enter attaches as-is · s reboots to match")
	fmt.Fprint(gate.Writer, "❯ ")
	reader := bufio.NewReader(gate.Reader)
	runeValue, _, err := reader.ReadRune()
	if err != nil {
		return false, err
	}
	fmt.Fprintln(gate.Writer)
	return runeValue == 's' || runeValue == 'S', nil
}

type DeviceGate struct{}

func (DeviceGate) Confirm(
	ctx context.Context,
	request GateRequest,
) (confirmed bool, returnErr error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		// No controlling terminal at all, or this process cannot open its
		// own — the ordinary shape of a detached/service invocation. Not
		// worth a line: every such caller hits this every time.
		return false, nil
	}
	if err != nil {
		// Anything else (ENXIO from a process with no ctty despite a
		// resolvable /dev/tty entry, a transient device error) is a probe
		// that could not run, not the ordinary "no terminal" case above —
		// name it so a gate that silently never fires is diagnosable.
		obs.Logger(ctx).WarnContext(ctx, "device gate: open /dev/tty failed", "err", err)
		return false, nil
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close controlling terminal: %w", err))
		}
	}()
	settings, err := unix.IoctlGetTermios(int(terminal.Fd()), getTermios)
	if err != nil {
		obs.Logger(ctx).WarnContext(ctx, "device gate: read terminal settings failed", "err", err)
		return false, nil
	}
	oneKey := *settings
	oneKey.Lflag &^= unix.ICANON | unix.ECHO
	oneKey.Cc[unix.VMIN] = 1
	oneKey.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(
		int(terminal.Fd()),
		setTermios,
		&oneKey,
	); err != nil {
		obs.Logger(ctx).WarnContext(ctx, "device gate: set raw terminal mode failed", "err", err)
		return false, nil
	}
	defer func() {
		if err := unix.IoctlSetTermios(int(terminal.Fd()), setTermios, settings); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore controlling terminal settings: %w", err))
		}
	}()
	return ReaderGate{Reader: terminal, Writer: terminal}.Confirm(ctx, request)
}

func cacheName(cache1H bool) string {
	if cache1H {
		return "⚡ 1h"
	}
	return "🪫 5m"
}
