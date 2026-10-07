package naming

import (
	"strconv"
	"strings"
	"unicode"
)

// WorkbenchPrefix turns a title into the prefix of its numbered chat names.
func WorkbenchPrefix(title string) string {
	prefix := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, strings.ToUpper(title))
	if prefix == "" {
		return "WORKBENCH"
	}
	return prefix
}

// NextNumbered fills the first gap among exact numbered names.
func NextNumbered(prefix string, taken []string) string {
	names := make(map[string]bool, len(taken))
	for _, name := range taken {
		names[name] = true
	}
	for n := 1; ; n++ {
		name := prefix + ":" + strconv.Itoa(n)
		if !names[name] {
			return name
		}
	}
}
