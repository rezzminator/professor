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

// LabelSoleTab is every name writer's shared policy for a chat name a user
// chose that changed (pfm chat name, a Claude /rename, launch --name): the
// name is pfm's again, whoever set the label before. Its
// persistent base name and flock are also used by the status painter, across
// plugin/hook processes.
func LabelSoleTab(ctx context.Context, socket, tab, label string, seq func() uint64) error {
	return labelTabByPolicy(ctx, socket, tab, label, true, seq)
}

// ReannounceSoleTab is LabelSoleTab for a name the chat already carried (a
// session start, a client move, a re-announce) or one no user chose (an
// automatic name, herdr.ChatNameAutoOption): no rename, so a label the user
// set in Herdr since pfm last wrote the tab stays.
func ReannounceSoleTab(ctx context.Context, socket, tab, label string, seq func() uint64) error {
	return labelTabByPolicy(ctx, socket, tab, label, false, seq)
}

func labelTabByPolicy(ctx context.Context, socket, tab, label string, explicit bool, seq func() uint64) error {
	if tab == "" {
		return nil
	}
	home, err := (paths.OSEnv{}).Home()
	if err != nil {
		return fmt.Errorf("tab label home: %w", err)
	}
	return writeSoleTabLabel(ctx, socket, paths.HerdrLockDir(home), tab, label, explicit, seq, Call)
}

// LabelPaneTab labels the tab pane sits in, found by pane.get, through
// LabelSoleTab when explicit says a user chose the name, else through
// ReannounceSoleTab, so a label the user set in Herdr stays: a tab the pane
// shares with another answers ErrTabNotSole.
func LabelPaneTab(ctx context.Context, socket, pane, label string, explicit bool) error {
	body, err := SeatRequest(ctx, socket, "pane.get", map[string]string{"pane_id": pane})
	if err != nil {
		return fmt.Errorf("read Herdr pane %s: %w", pane, err)
	}
	var reply struct {
		Pane struct {
			ID  string `json:"pane_id"`
			Tab string `json:"tab_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return fmt.Errorf("decode Herdr pane %s: %w", pane, err)
	}
	if reply.Pane.ID != pane {
		return fmt.Errorf("herdr pane.get for %s returned unexpected identity: %s", pane, body)
	}
	var seq uint64
	next := func() uint64 { seq++; return seq }
	write := ReannounceSoleTab
	if explicit {
		write = LabelSoleTab
	}
	if err := write(ctx, socket, reply.Pane.Tab, label, next); err != nil {
		return fmt.Errorf("tab %s of pane %s: %w", reply.Pane.Tab, pane, err)
	}
	return nil
}

// ErrTabNotSole is the named skip of a tab the chat's pane shares with
// another pane: its label is the user's, so no name writer relabels it.
var ErrTabNotSole = errors.New("the chat's pane is not alone in its tab")

// tabLabelRecord is a tab's label state: Base the name decorated, Rendered
// the label pfm last wrote, Seen the chat name the status painter last saw
// for it (empty until the painter follows the chat's name).
type tabLabelRecord struct {
	Base, Rendered, Identity, State, Mode, Seen string
	// Frame is the frame of State's markers Rendered drew (tabFrame).
	Frame int
	// UserSet says Base is a label the user set in Herdr, adopted by the
	// status painter: no later automatic change of the chat's composed name
	// replaces it, only a name the user chose does: an explicit pfm rename
	// (LabelSoleTab) or a change of the chat's user name (SeenUser).
	UserSet bool
	// UnseenExplicit protects an explicit pfm rename before the painter's
	// first source observation, which can still carry the pre-rename title.
	UnseenExplicit bool `json:",omitzero"`
	// SeenUser is the chat's user name (TabDecoration.UserName) the status
	// painter last saw on a row read from its source (UserKnown), UserSeen
	// whether it saw such a row at all: a record written before the painter
	// followed user names, or one only booting rows reached, starts following
	// without a rename.
	SeenUser string
	UserSeen bool
	// Pending is the label of a tab.rename whose answer was lost: Herdr may
	// have applied it. The next writer settles it against the tab's label
	// (settlePending) before it reads the label as anyone else's.
	Pending string
	// Glyphs are the tab glyphs the status painter last drew with, so a
	// rename between its ticks keeps the theme's prefix.
	Glyphs TabGlyphs `json:",omitzero"`
}

// owns says label is one pfm rendered for this record: the label it last
// wrote, or the record's base under any frame of its state, so a tab caught
// mid-animation (a rename applied whose record was not yet stored, a frame a
// concurrent writer drew) never reads as a label the user set.
func (record *tabLabelRecord) owns(label string) bool {
	if label == record.Rendered {
		return true
	}
	if record.Rendered == "" {
		return false
	}
	for frame := range tabMarkers(record.State, record.Mode, record.Glyphs) {
		if label == DecoratedTabLabel(record.Base, record.State, record.Mode, frame, record.Glyphs) {
			return true
		}
	}
	return false
}

// settlePending resolves a rename whose answer was lost: a tab label equal to
// the pending one says Herdr applied it, so it is the label pfm rendered; any
// other label says it did not, or a later rename replaced it. Either way the
// rename is settled; settled says the record changed.
func (record *tabLabelRecord) settlePending(label string) (settled bool) {
	if record.Pending == "" {
		return false
	}
	if label == record.Pending {
		record.Rendered = record.Pending
	}
	record.Pending = ""
	return true
}

// renameFailed stores want as the record's pending rename after a tab.rename
// whose call failed, so a lost answer is settled by the next writer instead of
// read as the user's label; the call's error comes back, joined with any
// failure to store the record. A rename a theme off stood down never left, and
// the record stays as the theme off finds it.
func renameFailed(path string, record tabLabelRecord, want string, err error) error {
	if errors.Is(err, errThemeOff) {
		return err
	}
	record.Pending = want
	return errors.Join(err, writeTabLabel(path, record))
}

func withTabLabelLock(ctx context.Context, dir, socket, tab string, work func(string) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create tab label directory: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("tab-%x", sha256.Sum256([]byte(socket+"\x00"+tab))))
	return withFlock(ctx, path+".lock", "tab label", func() error { return work(path + ".json") })
}

// withFlock runs work holding an exclusive flock on lockPath, polling for it
// until ctx ends; what names the lock in its errors. The lock opens through
// OpenStateFile, so a FIFO, a directory or a symlink at lockPath is a named
// error, never a hang.
func withFlock(ctx context.Context, lockPath, what string, work func() error) (returnErr error) {
	file, err := OpenStateFile(filepath.Dir(lockPath), filepath.Base(lockPath), os.O_CREATE|os.O_RDWR, what+" lock")
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("lock %s: %w", what, err)
		}
		if err := clock.Real.Sleep(ctx, 10*time.Millisecond); err != nil {
			return fmt.Errorf("wait for %s lock: %w", what, err)
		}
	}
	return work()
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
	explicit bool,
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
		if info.TabID != tab {
			return fmt.Errorf("tab.get for %s returned tab %s", tab, info.TabID)
		}
		if info.PaneCount != 1 {
			return fmt.Errorf("tab %s holds %d panes: %w", tab, info.PaneCount, ErrTabNotSole)
		}
		record, err := readTabLabel(path)
		if err != nil {
			return err
		}
		settled := record.settlePending(info.Label)
		if !explicit && (record.UserSet || record.Rendered != "" && !record.owns(info.Label)) {
			// An unchanged name re-announced is no rename: the user's label stays.
			if settled {
				return writeTabLabel(path, record)
			}
			return nil
		}
		if !record.owns(info.Label) {
			record = tabLabelRecord{}
		}
		// An explicit rename: the name is pfm's again, whoever set the label before.
		base := TabLabel(label)
		switch {
		case explicit:
			record.UnseenExplicit = record.Seen == ""
		case base != record.Base:
			// Another name no user chose replaces the explicit one: nothing left to protect.
			record.UnseenExplicit = false
		}
		// A re-announce of the same name keeps an unseen explicit rename protected.
		record.Base, record.UserSet = base, false
		want := DecoratedTabLabel(record.Base, record.State, record.Mode, record.Frame, record.Glyphs)
		if info.Label != want {
			rename, err := (TabRename{Tab: tab, Label: want, Seq: seq()}).Line()
			if err != nil {
				return err
			}
			if _, err := call(ctx, socket, rename); err != nil {
				return renameFailed(path, record, want, fmt.Errorf("relabel %q: %w", label, err))
			}
		}
		record.Rendered = want
		return writeTabLabel(path, record)
	})
}
