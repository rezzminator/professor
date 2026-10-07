package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// NameReservation is a producer's capability for one automatic chat name.
// Its nonce travels only through internal request context, never CLI arguments.
type (
	NameReservation struct {
		Name        string
		path, owner string
		retained    *atomic.Bool
	}
	workbenchNameReceipt struct {
		Name      string `json:"name"`
		Owner     string `json:"owner"`
		Committed bool   `json:"committed,omitempty"`
	}
	workbenchReservationContextKey struct{}
)

// WorkbenchName retains the name-only lookup interface for roster consumers.
func WorkbenchName(
	ctx context.Context,
	cwd string,
	warn io.Writer,
	runtime *pfmconfig.Runtime,
	reserved ...string,
) (string, bool, error) {
	claim, found, err := ReserveWorkbenchName(ctx, cwd, warn, runtime, reserved...)
	return claim.Name, found, err
}

// ReserveWorkbenchName exclusively reserves a name and returns its ownership.
func ReserveWorkbenchName(
	ctx context.Context,
	cwd string,
	warn io.Writer,
	runtime *pfmconfig.Runtime,
	reserved ...string,
) (claim NameReservation, found bool, returnErr error) {
	bench, found, err := workbench.Nearest(cwd)
	if err != nil || !found {
		return claim, false, err
	}
	if bench.Err != nil {
		return claim, false, bench.Err
	}
	rows, err := Rows(ctx, warn, runtime)
	if err != nil {
		obs.Logger(ctx).Error("workbench roster", "path", cwd, obs.FieldErr, err)
		return claim, false, err
	}
	names := make([]string, 0, len(rows)+len(reserved))
	for i := range rows {
		names = append(names, rows[i].Name)
	}
	names = append(names, reserved...)
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return claim, false, fmt.Errorf("resolve workbench claim root: %w", err)
	}
	dir := filepath.Join(effective.Paths.SIDDir, ".name-claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return claim, false, fmt.Errorf("create workbench claims %s: %w", dir, err)
	}
	lock, err := lockWorkbenchClaims(dir)
	if err != nil {
		return claim, false, err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	owner := make([]byte, 16)
	if _, err := rand.Read(owner); err != nil {
		return claim, false, fmt.Errorf("create workbench claim owner: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return claim, false, err
		}
		candidate := naming.NextNumbered(bench.Prefix, names)
		sum := sha256.Sum256([]byte(candidate))
		path := filepath.Join(dir, fmt.Sprintf("%x", sum))
		content, err := json.Marshal(workbenchNameReceipt{Name: candidate, Owner: fmt.Sprintf("%x", owner)})
		if err != nil {
			return claim, false, fmt.Errorf("encode workbench claim %s: %w", candidate, err)
		}
		err = atomicfile.Create(path, content, 0o600)
		if errors.Is(err, os.ErrExist) {
			receipt, readErr := readWorkbenchNameReceipt(path)
			if readErr != nil {
				return claim, false, readErr
			}
			if receipt.Name != candidate {
				return claim, false, fmt.Errorf("invalid workbench name claim %s", path)
			}
			names = append(names, candidate)
			continue
		}
		if err != nil {
			return claim, false, fmt.Errorf("reserve workbench name %s: %w", candidate, err)
		}
		return NameReservation{
			Name:     candidate,
			path:     path,
			owner:    fmt.Sprintf("%x", owner),
			retained: new(atomic.Bool),
		}, true, nil
	}
}

func lockWorkbenchClaims(dir string) (*os.File, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open workbench claim ownership %s: %w", dir, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("lock workbench claims %s: %w", dir, err), file.Close())
	}
	return file, nil
}

func readWorkbenchNameReceipt(path string) (workbenchNameReceipt, error) {
	var receipt workbenchNameReceipt
	raw, err := os.ReadFile(path)
	if err != nil {
		return receipt, fmt.Errorf("read workbench claim %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("parse workbench claim %s: %w", path, err)
	}
	if strings.TrimSpace(receipt.Name) == "" || receipt.Owner == "" {
		return receipt, fmt.Errorf("invalid workbench claim %s", path)
	}
	return receipt, nil
}

// Commit retains this producer's name after creation, including later failures.
func (claim NameReservation) Commit() error {
	if claim.retained != nil {
		claim.retained.Store(true)
	}
	return claim.edit(true)
}

// Release drops only this producer's unused claim; stale capabilities are harmless.
func (claim NameReservation) Release() error {
	if claim.retained != nil && claim.retained.Load() {
		return nil
	}
	return claim.edit(false)
}

func (claim NameReservation) edit(commit bool) (returnErr error) {
	if claim.path == "" {
		return nil
	}
	lock, err := lockWorkbenchClaims(filepath.Dir(claim.path))
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	receipt, err := readWorkbenchNameReceipt(claim.path)
	if errors.Is(err, os.ErrNotExist) {
		if !commit {
			return nil
		}
		return fmt.Errorf("workbench claim %s is no longer owned", claim.Name)
	}
	if err != nil {
		return err
	}
	if receipt.Name != claim.Name || receipt.Owner != claim.owner {
		if !commit {
			return nil
		}
		return fmt.Errorf("workbench claim %s belongs to another producer", claim.Name)
	}
	if receipt.Committed {
		return nil
	}
	if !commit {
		if err := os.Remove(claim.path); err != nil {
			return fmt.Errorf("release workbench claim %s: %w", claim.path, err)
		}
		return nil
	}
	receipt.Committed = true
	raw, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode committed workbench claim: %w", err)
	}
	return atomicfile.Write(claim.path, raw, 0o600)
}

// WithWorkbenchNameReservation hands an immutable capability to an in-process CLI dispatch.
func WithWorkbenchNameReservation(ctx context.Context, claim NameReservation) context.Context {
	return context.WithValue(ctx, workbenchReservationContextKey{}, claim)
}

// WorkbenchReservation returns only the capability handed to this request and name.
func WorkbenchReservation(ctx context.Context, name string) NameReservation {
	claim, _ := ctx.Value(workbenchReservationContextKey{}).(NameReservation)
	if claim.Name != name {
		return NameReservation{}
	}
	return claim
}

// ReleaseUnusedWorkbenchName reports cleanup of this request's unused reservation.
func ReleaseUnusedWorkbenchName(ctx context.Context, name string, warn io.Writer) {
	if err := WorkbenchReservation(ctx, name).Release(); err != nil {
		fmt.Fprintf(warn, "pfm: release unused workbench name: %v\n", err)
	}
}

// CommitWorkbenchLaunch preserves this request's claim once its process exists.
func CommitWorkbenchLaunch(ctx context.Context, created bool, name string, launchErr error) error {
	return WorkbenchReservation(ctx, name).settle(created, launchErr)
}

func (claim NameReservation) settle(created bool, launchErr error) error {
	var partial *spawn.SessionCreatedError
	if created || errors.As(launchErr, &partial) {
		if err := claim.Commit(); err != nil {
			return errors.Join(launchErr, fmt.Errorf("record live workbench name claim: %w", err))
		}
		return launchErr
	}
	return errors.Join(launchErr, claim.Release())
}
