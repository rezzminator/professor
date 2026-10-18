package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// LabelSoleTab is every name writer's shared policy. Its persistent base name
// and flock are also used by the status painter, across plugin/hook processes.
func LabelSoleTab(ctx context.Context, socket, tab, label string, seq func() uint64) error {
	if tab == "" {
		return nil
	}
	home, err := (paths.OSEnv{}).Home()
	if err != nil {
		return fmt.Errorf("tab label home: %w", err)
	}
	return writeSoleTabLabel(ctx, socket, paths.HerdrLockDir(home), tab, label, seq, Call)
}

type tabLabelRecord struct {
	Base, Rendered, Identity, State, Mode string
	On                                    bool
}

func withTabLabelLock(ctx context.Context, dir, socket, tab string, work func(string) error) (returnErr error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create tab label directory: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("tab-%x", sha256.Sum256([]byte(socket+"\x00"+tab))))
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open tab label lock: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("lock tab label: %w", err)
		}
		if err := clock.Real.Sleep(ctx, 10*time.Millisecond); err != nil {
			return fmt.Errorf("wait for tab label lock: %w", err)
		}
	}
	return work(path + ".json")
}

func readTabLabel(path string) (tabLabelRecord, error) {
	var record tabLabelRecord
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, fmt.Errorf("read tab label %s: %w", path, err)
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return record, fmt.Errorf("decode tab label %s: %w", path, err)
	}
	return record, nil
}

func writeTabLabel(path string, record tabLabelRecord) error {
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode tab label: %w", err)
	}
	if err := atomicfile.Write(path, body, 0o600); err != nil {
		return fmt.Errorf("store tab label %s: %w", path, err)
	}
	return nil
}

func writeSoleTabLabel(
	ctx context.Context,
	socket, dir, tab, label string,
	seq func() uint64,
	call func(context.Context, string, ...[]byte) ([]json.RawMessage, error),
) error {
	if tab == "" {
		return nil
	}
	return withTabLabelLock(ctx, dir, socket, tab, func(path string) error {
		get, err := (TabGet{Tab: tab, Seq: seq()}).Line()
		if err != nil {
			return err
		}
		results, err := call(ctx, socket, get)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if len(results) != 1 {
			return fmt.Errorf("read tab %s: got %d results", tab, len(results))
		}
		info, err := ParseTab(results[0])
		if err != nil {
			return err
		}
		if info.TabID != tab || info.PaneCount != 1 {
			return nil
		}
		record, err := readTabLabel(path)
		if err != nil {
			return err
		}
		if info.Label != record.Rendered {
			record = tabLabelRecord{}
		}
		record.Base = TabLabel(label)
		want := DecoratedTabLabel(record.Base, record.State, record.Mode, record.On)
		if info.Label != want {
			rename, err := (TabRename{Tab: tab, Label: want, Seq: seq()}).Line()
			if err != nil {
				return err
			}
			if _, err := call(ctx, socket, rename); err != nil {
				return fmt.Errorf("relabel %q: %w", label, err)
			}
		}
		record.Rendered = want
		return writeTabLabel(path, record)
	})
}
