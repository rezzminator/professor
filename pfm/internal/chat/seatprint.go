package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// SeatPrinter reads a followed Claude seat's fingerprint: a condensation of
// every input its fleet listing row rests on, read without a fleet scan, so a
// watch reuses the seat's row while the fingerprint holds
// (headless.FleetWatcher.Fingerprint).
//
// The inputs, from the fleet scan's own routes (compose's crumb and agent
// routes, gather's probes):
//   - the seat's engine: the process at the root of its pane, its argv
//     (the session it resumes) and its start; a seat whose pane root is not
//     Claude itself (a shell that started it) is never fingerprinted, since
//     the engine could end while the pane lives on;
//   - the box's tmux sockets by name: a respawn, a vanished socket or a
//     second server for the same conversation changes the set;
//   - every crumb's name, size and modification time (the files the scan's
//     crumb route reads, nothing else in their directory): a /clear, a
//     resume, a new or departed seat rewrites one;
//   - the transcript's directory: a continuation or a new conversation adds a
//     file to it.
//
// A pane or session renamed inside a live server is not among them; the
// watch's periodic scan (headless.SeatPrintReuse) catches it.
type SeatPrinter struct {
	tmuxDir string
	sidDir  string
	proc    gather.ProcFS
	binary  string
}

// NewSeatPrinter reads the machine's paths and Claude binary once.
func NewSeatPrinter(runtime *pfmconfig.Runtime) (*SeatPrinter, error) {
	machine, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return nil, fmt.Errorf("seat fingerprint: %w", err)
	}
	return &SeatPrinter{
		tmuxDir: machine.Paths.TmuxDir,
		sidDir:  machine.Paths.SIDDir,
		proc:    gather.NewProcFS(machine.Paths.ProcRoot),
		binary:  machine.Config.Claude.Binary,
	}, nil
}

// Print is chat's fingerprint, ok false when chat is no live Claude seat
// rooted in its own pane. Any read that fails is a changed seat — ok false,
// and the watch scans — never a fingerprint that hides the failure.
func (printer *SeatPrinter) Print(chat headless.Chat) (string, bool) {
	if !chat.Live || chat.Engine != pfmengine.Claude || chat.Socket == "" || chat.Pane == "" ||
		chat.PanePID <= 0 || chat.Path == "" {
		return "", false
	}
	argv, err := printer.proc.Cmdline(chat.PanePID)
	if err != nil || !gather.IsClaudeCommand(argv, printer.binary) {
		return "", false
	}
	stat, err := printer.proc.Stat(chat.PanePID)
	if err != nil {
		return "", false
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "engine=%d %d %q\n", chat.PanePID, stat.StartTime, argv)
	sockets, err := os.ReadDir(printer.tmuxDir)
	if err != nil {
		return "", false
	}
	for _, entry := range sockets {
		fmt.Fprintf(hash, "socket=%s\n", entry.Name())
	}
	crumbs, err := os.ReadDir(printer.sidDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	for _, entry := range crumbs {
		// Only what the scan's crumb route reads (gather.ReadCrumbs): the
		// statusline's effort records and their scratch files, rewritten on
		// every render of every chat, are no input of the row.
		if _, _, crumb := gather.ParseCrumbName(entry.Name()); !crumb || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", false
		}
		fmt.Fprintf(hash, "crumb=%s %d %d\n", entry.Name(), info.Size(), info.ModTime().UnixNano())
	}
	directory, err := os.Stat(filepath.Dir(chat.Path))
	if err != nil {
		return "", false
	}
	fmt.Fprintf(hash, "transcripts=%d\n", directory.ModTime().UnixNano())
	return hex.EncodeToString(hash.Sum(nil)), true
}
