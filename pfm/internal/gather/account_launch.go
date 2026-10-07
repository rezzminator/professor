package gather

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

const AccountLaunchClaimsName = ".pfm-launches"

// AccountGuard serializes launch registration and install's activity scan with
// account rewiring. A launch claim outlives the actor and identifies a process
// generation, so delayed engine session markers and reused PIDs are safe.
type (
	AccountGuard struct {
		file       *os.File
		dir, claim string
	}
	accountLaunchClaim struct {
		PID     int    `json:"pid"`
		Start   uint64 `json:"start"`
		Pending bool   `json:"pending,omitempty"`
	}
)

// AcquireAccountGuard waits for parent launch registration when launching,
// while install refuses a busy account visibly. Claims precede process creation.
func AcquireAccountGuard(dir string, launching bool) (_ *AccountGuard, err error) {
	if dir == "" {
		return nil, nil
	}
	file, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open account ownership %s: %w", dir, err)
	}
	lock := syscall.LOCK_EX
	if !launching {
		lock |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), lock); err != nil {
		return nil, errors.Join(fmt.Errorf("account ownership %s is busy: %w", dir, err), file.Close())
	}
	guard := &AccountGuard{file: file, dir: dir}
	if !launching {
		return guard, nil
	}
	claims := filepath.Join(dir, AccountLaunchClaimsName)
	if err := os.MkdirAll(claims, 0o700); err != nil {
		return nil, errors.Join(fmt.Errorf("create launch claims %s: %w", claims, err), guard.Close())
	}
	guard.claim = filepath.Join(claims, fmt.Sprintf("%d-%016x", os.Getpid(), rand.Uint64()))
	if err := atomicfile.Create(guard.claim, []byte(`{"pending":true}`), 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("prepare account launch claim %s: %w", guard.claim, err), guard.Close())
	}
	return guard, nil
}

// Record registers the real child's (or exec's) PID while ownership is held.
// A child already gone requires no claim. Any other failure retains the pending
// claim unless the caller proves it terminated the child and calls Abort.
func (guard *AccountGuard) Record(pid int) error {
	if guard == nil {
		return nil
	}
	if pid <= 0 {
		return fmt.Errorf("record account launch: invalid pid %d", pid)
	}
	stat, err := NewProcFS("").Stat(pid)
	if errors.Is(err, fs.ErrNotExist) {
		return guard.Abort()
	}
	if err != nil {
		return fmt.Errorf("read launch process %d generation: %w", pid, err)
	}
	raw, err := json.Marshal(accountLaunchClaim{PID: pid, Start: stat.StartTime})
	if err != nil {
		return fmt.Errorf("encode account launch %d: %w", pid, err)
	}
	if err := atomicfile.Write(guard.claim, raw, 0o600); err != nil {
		return fmt.Errorf("record account launch %s: %w", guard.claim, err)
	}
	return nil
}

// Active returns live claims and removes only proven exited generations.
// Enumeration, unreadable, malformed, and unresolved claims are errors.
func (guard *AccountGuard) Active(proc ProcFS) ([]int, error) {
	if guard == nil {
		return nil, nil
	}
	dir := filepath.Join(guard.dir, AccountLaunchClaimsName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect account launch claims %s: %w", dir, err)
	}
	var live []int
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read account launch claim %s: %w", path, err)
		}
		var claim accountLaunchClaim
		if err := json.Unmarshal(raw, &claim); err != nil {
			return nil, fmt.Errorf("parse account launch claim %s: %w", path, err)
		}
		if claim.Pending || claim.PID <= 0 || claim.Start == 0 {
			return nil, fmt.Errorf(
				"unresolved account launch claim %s; inspect the failed launch before removing this claim",
				path,
			)
		}
		stat, err := proc.Stat(claim.PID)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspect account launch claim %s pid %d: %w", path, claim.PID, err)
		}
		if err == nil && stat.StartTime == claim.Start {
			live = append(live, claim.PID)
			continue
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove exited account launch claim %s: %w", path, err)
		}
	}
	return live, nil
}

// Abort removes this actor's claim only after no live child can remain.
func (guard *AccountGuard) Abort() error {
	if guard == nil || guard.claim == "" {
		return nil
	}
	if err := os.Remove(guard.claim); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove account launch claim %s: %w", guard.claim, err)
	}
	guard.claim = ""
	return nil
}

// Close releases ownership without discarding a live or unresolved claim.
func (guard *AccountGuard) Close() error {
	if guard == nil || guard.file == nil {
		return nil
	}
	err := guard.file.Close()
	guard.file = nil
	if err != nil {
		return fmt.Errorf("release account ownership %s: %w", guard.dir, err)
	}
	return nil
}
