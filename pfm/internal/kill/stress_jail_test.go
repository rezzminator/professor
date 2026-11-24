//go:build jail

package kill

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestStressTwentyKillUnkillProcesses(t *testing.T) {
	jail := newKillJail(t)
	testjail.DatabaseFiles(t, jail.root) // the helper processes open the same files
	database := jail.open(t)
	ctx := testwait.Context(t)
	if err := database.Batch(
		ctx,
		killStressProcesses*killStressIDs*2,
		func(tx *store.ImmediateTx, start, end int) error {
			for index := start; index < end; index++ {
				process := index / (killStressIDs * 2)
				offset := (index / 2) % killStressIDs
				kind := "keep"
				if index%2 == 1 {
					kind = "drop"
				}
				id := fmt.Sprintf("%s-%02d-%02d", kind, process, offset)
				if err := tx.UpsertTranscript(ctx, store.Transcript{
					UUID: id,
					Path: "/jailed/" + id + ".jsonl",
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	gate := filepath.Join(jail.root, "kill-stress-go")
	processes := make([]*killStressProcess, 0, killStressProcesses)
	readyPaths := make([]string, 0, killStressProcesses)
	for index := 0; index < killStressProcesses; index++ {
		ready := filepath.Join(jail.root, fmt.Sprintf("ready-%02d", index))
		command := exec.CommandContext(
			ctx,
			os.Args[0],
			"-test.run=^TestKillStressProcessHelper$",
		)
		command.Env = append(
			os.Environ(),
			killStressHelperEnv+"=1",
			fmt.Sprintf("%s=%d", killStressIndexEnv, index),
			killStressReadyEnv+"="+ready,
			killStressGateEnv+"="+gate,
			killStressHomeEnv+"="+jail.home,
		)
		process := &killStressProcess{command: command}
		command.Stdout = &process.output
		command.Stderr = &process.output
		if err := command.Start(); err != nil {
			t.Fatalf("start helper %d: %v", index, err)
		}
		processes = append(processes, process)
		readyPaths = append(readyPaths, ready)
	}
	t.Cleanup(func() {
		for _, process := range processes {
			if process.command.Process != nil {
				_ = process.command.Process.Kill()
			}
		}
	})
	waitKillStressPaths(t, readyPaths, processes)
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := 0
	for index, process := range processes {
		if err := process.command.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", index, err, process.output.String())
		}
		warnings += strings.Count(process.output.String(), "WARNING:")
	}

	database = jail.open(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := killStressProcesses * killStressIDs
	if len(killed) != want {
		t.Fatalf(
			"killed count=%d, want=%d; warnings=%d",
			len(killed),
			want,
			warnings,
		)
	}
	for _, row := range killed {
		if !strings.HasPrefix(row.ID, "keep-") {
			t.Fatalf("unkill lost for %#v", row)
		}
	}
	if warnings != 0 {
		t.Fatalf(
			"contention dropped accepted writes: warnings=%d, want 0",
			warnings,
		)
	}
	t.Logf(
		"STRESS kill/unkill processes=%d operations=%d final=%d warnings=%d",
		killStressProcesses,
		killStressProcesses*killStressIDs*3,
		len(killed),
		warnings,
	)
}

type killStressProcess struct {
	command *exec.Cmd
	output  bytes.Buffer
}

func waitKillStressPaths(t *testing.T, paths []string, processes []*killStressProcess) {
	t.Helper()
	testwait.Until(t, "the stress helpers to report ready", func() (bool, string) {
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				return false, fmt.Sprintf("%s: %v\n%s", path, err, killStressOutputs(processes))
			}
		}
		return true, ""
	})
}

func killStressOutputs(processes []*killStressProcess) string {
	var output strings.Builder
	for index, process := range processes {
		fmt.Fprintf(&output, "\nhelper %d:\n%s", index, process.output.String())
	}
	return output.String()
}
