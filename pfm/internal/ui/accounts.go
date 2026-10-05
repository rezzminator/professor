package ui

import (
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Account roster helpers: pure functions over the id lists a snapshot carries.

func defaultNewChatEngine(claude, codex, openCode []int) pfmengine.ID {
	if len(normalizedAccountIDs(claude)) != 0 ||
		(len(normalizedAccountIDs(codex)) == 0 && len(normalizedAccountIDs(openCode)) == 0) {
		return pfmengine.Claude
	}
	if len(normalizedAccountIDs(codex)) != 0 {
		return pfmengine.Codex
	}
	return pfmengine.OpenCode
}

func copyEmojis(values map[int]string) map[int]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[int]string, len(values))
	for id, emoji := range values {
		result[id] = emoji
	}
	return result
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func validAccount(account int, roster []int) int {
	ids := normalizedAccountIDs(roster)
	for _, id := range ids {
		if account == id {
			return account
		}
	}
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func normalizedAccountIDs(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	result := make([]int, 0, len(values))
	seen := make(map[int]bool, len(values))
	for _, value := range values {
		if value > 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func nextAccount(current int, ids []int) int {
	if len(ids) == 0 {
		return 0
	}
	for index, id := range ids {
		if id == current {
			return ids[(index+1)%len(ids)]
		}
	}
	return ids[0]
}
