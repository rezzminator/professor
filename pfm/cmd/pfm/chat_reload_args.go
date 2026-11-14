package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/reload"
)

func validateReloadArgs(args []string) error {
	account := false
	newSeat := false
	hide := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case reloadNewFlag:
			if newSeat {
				return errors.New("new specified twice")
			}
			newSeat = true
		case reloadHideFlag:
			if hide {
				return errors.New("hide specified twice")
			}
			hide = true
		case reloadThenFlag, reloadSocketFlag, reloadPaneFlag, reloadModelFlag, reloadEffortFlag:
			// --pane is worker-only plumbing (see reloadTarget): accepted here
			// because this same validator runs on the worker's expanded argv,
			// but it is deliberately absent from reload.Usage and
			// reloadArgumentHint — no caller-facing doc ever tells a human or
			// a model to pass it.
			if index+1 >= len(args) {
				return fmt.Errorf("%s needs a value", args[index])
			}
			index++
		case reloadAccountFlag:
			if index+1 >= len(args) {
				return errors.New("--account needs an account number, as in --account 2")
			}
			if !positiveAccount(args[index+1]) {
				return fmt.Errorf(
					"--account takes an account NUMBER, not %q — see `pfm config show` for the configured accounts",
					args[index+1],
				)
			}
			if account {
				return errors.New("account specified twice")
			}
			account = true
			index++
		case reloadCacheFlag:
			if index+1 >= len(args) || (args[index+1] != "1h" && args[index+1] != "5m") {
				return errors.New("--cache must be 1h|5m")
			}
			index++
		default:
			if !positiveAccount(args[index]) {
				return errors.New(reloadArgumentHint(args[index]))
			}
			if account {
				return errors.New("account specified twice")
			}
			account = true
		}
	}
	if hide && !newSeat {
		return errors.New("--hide needs --new — a reload that resumes the same conversation cannot hide it")
	}
	return nil
}

// reloadRequestedAccount returns the account number a validated reload argv
// asks for, through --account N or a bare positive number, or 0 when it names
// none. The front checks it against the roster before scheduling the worker.
func reloadRequestedAccount(args []string) int {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case reloadNewFlag, reloadHideFlag:
		case reloadThenFlag, reloadSocketFlag, reloadPaneFlag, reloadModelFlag, reloadEffortFlag, reloadCacheFlag:
			index++
		case reloadAccountFlag:
			if index+1 < len(args) {
				if account, err := strconv.Atoi(args[index+1]); err == nil && account > 0 {
					return account
				}
			}
			index++
		default:
			if account, err := strconv.Atoi(args[index]); err == nil && account > 0 {
				return account
			}
		}
	}
	return 0
}

// reloadArgumentHint turns a rejected word into an error the CALLER can act on
// without re-reading the usage line and guessing again.
//
// The usage string alone was not enough: a caller told "reload the cache off"
// sent `reload cache off`, got the bare usage back, and had to work out on its
// own that "cache" meant --cache. An error that only restates the grammar makes
// the reader do the mapping the command already knows how to do.
func reloadArgumentHint(argument string) string {
	suggestion := ""
	switch strings.ToLower(strings.TrimPrefix(argument, "--")) {
	case "cache", "1h", "ttl", "prompt-cache":
		suggestion = "did you mean --cache 1h|5m?"
	case "account", "acct", "seat", "profile":
		suggestion = "did you mean --account N?"
	case "fresh", newAction, "restart", "reset":
		suggestion = "did you mean --new?"
	case "hide", "kill", "close", "forget":
		suggestion = "did you mean --hide? (beside --new: hides the conversation left behind)"
	case thenAction, "prompt", "continue":
		suggestion = "did you mean --then \"prompt\"?"
	case "sock", "socket", chatCommand, "target":
		suggestion = "did you mean --sock socket? (omit it and the calling chat is detected automatically)"
	case "model":
		suggestion = "did you mean --model NAME?"
	case "effort", "level", "reasoning", "thinking":
		suggestion = "did you mean --effort LEVEL?"
	}
	if suggestion == "" {
		suggestion = "an account is passed as --account N, and every other setting has its own flag"
	}
	return fmt.Sprintf("%q is not a reload argument — %s\n%s", argument, suggestion, reload.Usage)
}
