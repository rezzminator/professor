package call

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func applyEnvironment(options *Options, env paths.Env, warn io.Writer) {
	seconds := []struct {
		name   string
		target *time.Duration
	}{
		{"CHAT_CALL_POLL", &options.Poll},
		{"CHAT_CALL_ENTER_SETTLE", &options.EnterSettle},
		{"CHAT_CALL_PROOF_SETTLE", &options.ProofSettle},
		{"CHAT_CALL_LOCK_TIMEOUT", &options.LockTimeout},
		{"CHAT_CALL_LOCK_POLL", &options.LockPoll},
		{"CHAT_CALL_LOCK_MAXHOLD", &options.LockMaxHold},
		{"CHAT_CALL_COMMAND_GAP", &options.CommandChunkGap},
		{"CHAT_CALL_TYPIST_QUIET", &options.TypistQuiet},
		{"CHAT_THEN_MIN", &options.ThenMin},
		{"CHAT_THEN_IDLE_POLL", &options.ThenIdlePoll},
		{"CHAT_THEN_SETTLE", &options.ThenSettle},
	}
	for _, setting := range seconds {
		value := env.Get(setting.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 {
			fmt.Fprintf(
				warn,
				"pfm: ignoring %s=%q: want seconds >= 0; using %s\n",
				setting.name,
				value,
				*setting.target,
			)
			continue
		}
		*setting.target = time.Duration(parsed * float64(time.Second))
	}
	integers := []struct {
		name   string
		target *int
	}{
		{"CHAT_CALL_BUSY_TRIES", &options.BusyTries},
		{"CHAT_CALL_INTR_TRIES", &options.InterruptTries},
		{"CHAT_CALL_STASH_TRIES", &options.StashTries},
		{"CHAT_CALL_SETTLE_TRIES", &options.SettleTries},
		{"CHAT_CALL_ENTER_TRIES", &options.EnterTries},
		{"CHAT_CALL_SCROLLBACK", &options.Scrollback},
		{"CHAT_CALL_PROOF_LINES", &options.ProofLines},
		{"CHAT_CALL_COMMAND_CHUNK", &options.CommandChunkRunes},
		{"CHAT_THEN_BUSY_TRIES", &options.ThenBusyTries},
		{"CHAT_THEN_IDLE_TRIES", &options.ThenIdleTries},
		{"CHAT_THEN_IDLE_STABLE", &options.ThenIdleStable},
	}
	for _, setting := range integers {
		*setting.target = PositiveIntEnv(env, setting.name, *setting.target, warn)
	}
}

// PositiveIntEnv reads the positive integer knob name from env. Unset is
// fallback; any other value that is not a positive integer is fallback too,
// said on warn, so a typo never passes as the default.
func PositiveIntEnv(env paths.Env, name string, fallback int, warn io.Writer) int {
	value := env.Get(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		fmt.Fprintf(warn, "pfm: ignoring %s=%q: want a positive integer; using %d\n", name, value, fallback)
		return fallback
	}
	return parsed
}
