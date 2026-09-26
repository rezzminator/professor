package engine

import (
	"strconv"
	"strings"
)

// SocketBirth is the inverse of spawn.FreshSocket: the unix second a chat's
// tmux socket name was minted in, read back out of the name itself
// ("ox-1789813424-3207648-22387" -> 1789813424).
//
// It is the ONE parser of that shape (K3). A socket whose prefix belongs to no
// registered engine, whose arity is not the four fields FreshSocket writes, or
// whose epoch field is not a non-negative integer returns 0 — "this name
// carries no birth", which every caller already treats as "fall back to
// another activity signal".
func SocketBirth(name string) int64 {
	parts := strings.Split(name, "-")
	if len(parts) != 4 {
		return 0
	}
	if _, known := FromSocket(name); !known {
		return 0
	}
	epoch, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || epoch < 0 {
		return 0
	}
	return epoch
}
