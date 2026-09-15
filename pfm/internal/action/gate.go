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
		return false, nil
	}
	if err != nil {
		return false, nil
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close controlling terminal: %w", err))
		}
	}()
	settings, err := unix.IoctlGetTermios(int(terminal.Fd()), getTermios)
	if err != nil {
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
