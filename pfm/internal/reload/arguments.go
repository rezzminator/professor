package reload

import (
	"fmt"
	"strings"
)

// The /reload flags, as a person types them and the worker's argv carries them.
const (
	ArgumentAccount = "--account"
	ArgumentModel   = "--model"
	ArgumentEffort  = "--effort"
	ArgumentCache   = "--cache"
	ArgumentNew     = "--new"
	ArgumentHide    = "--hide"
	ArgumentThen    = "--then"
	ArgumentSocket  = "--sock"
	ArgumentPane    = "--pane"
)

// NormalizeArguments maps every cache spelling a person reaches for onto the
// one canonical --cache 1h|5m before validation: `--1h` and `--5m`, each with
// an optional trailing `on`, and `--cache on|off`, since the launch knob is
// the cache1h boolean. A flag's value is copied untouched, so `--then --1h`
// keeps its prompt. The worker's argv is normalized again, idempotently.
func NormalizeArguments(args []string) []string {
	normalized := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		switch word := args[index]; word {
		case ArgumentThen, ArgumentSocket, ArgumentPane, ArgumentModel, ArgumentEffort, ArgumentAccount:
			normalized = append(normalized, word)
			if index+1 < len(args) {
				index++
				normalized = append(normalized, args[index])
			}
		case "--1h", "--5m":
			normalized = append(normalized, ArgumentCache, strings.TrimPrefix(word, "--"))
			if index+1 < len(args) && strings.EqualFold(args[index+1], "on") {
				index++
			}
		case ArgumentCache:
			normalized = append(normalized, word)
			if index+1 < len(args) {
				index++
				value := strings.ToLower(args[index])
				switch value {
				case "on":
					value = "1h"
				case "off":
					value = "5m"
				}
				normalized = append(normalized, value)
			}
		default:
			normalized = append(normalized, word)
		}
	}
	return normalized
}

// WorkerArguments carries the validated CLI values to a backend-specific request.
type WorkerArguments struct {
	Account, Cache, Socket, Pane, Then, Model, Effort string
	New, Hide                                         bool
}

// ParseWorkerArguments decodes values after the CLI's product validation.
func ParseWorkerArguments(args []string) (WorkerArguments, error) {
	var parsed WorkerArguments
	values := map[string]*string{
		ArgumentAccount: &parsed.Account,
		ArgumentCache:   &parsed.Cache,
		ArgumentSocket:  &parsed.Socket,
		ArgumentPane:    &parsed.Pane,
		ArgumentThen:    &parsed.Then,
		ArgumentModel:   &parsed.Model,
		ArgumentEffort:  &parsed.Effort,
	}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case ArgumentNew:
			parsed.New = true
		case ArgumentHide:
			parsed.Hide = true
		default:
			if dest, ok := values[args[index]]; ok {
				if index+1 >= len(args) {
					return parsed, fmt.Errorf("%s needs a value", args[index])
				}
				index++
				*dest = args[index]
			} else {
				parsed.Account = args[index]
			}
		}
	}
	return parsed, nil
}
