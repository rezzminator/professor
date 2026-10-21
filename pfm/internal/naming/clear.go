package naming

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/engine"
)

// ClearName is pfm's durable name for a /clear successor. Emitted means the
// first prompt received Claude's supported sessionTitle output.
type ClearName struct {
	Inherited string `json:"inherited"`
	Name      string `json:"name"`
	Emitted   bool   `json:"emitted"`
	Confirmed bool   `json:"confirmed"`
}

// ClearSuccessorName chooses the smallest free numbered name for the base.
// The caller must read occupied names and reserve the result in one transaction.
// The inherited name counts as occupied even before its transcript is indexed.
func ClearSuccessorName(original, sessionID string, occupied ...string) (string, error) {
	if !engine.IsUUID(sessionID) {
		return "", fmt.Errorf("clear successor %q is not a session id", sessionID)
	}
	if strings.TrimSpace(original) == "" {
		return "", nil
	}
	if strings.ContainsAny(original, "\x00\r\n") {
		return "", fmt.Errorf("clear predecessor name %q is not one line", original)
	}
	used := make(map[string]bool, len(occupied)+1)
	used[original] = true
	for _, name := range occupied {
		used[name] = true
	}
	base, _ := clearNameParts(original)
	for number := 2; ; number++ {
		candidate := base + " #" + strconv.Itoa(number)
		if !used[candidate] {
			return candidate, nil
		}
	}
}

// Validate checks a stored decision's shape, not a fresh allocation: other
// sessions may have claimed or released names since this choice was committed.
func (name ClearName) Validate(sessionID string) error {
	if _, err := ClearSuccessorName(name.Inherited, sessionID); err != nil {
		return err
	}
	base, _ := clearNameParts(name.Inherited)
	chosenBase, number := clearNameParts(name.Name)
	if strings.TrimSpace(base) == "" || chosenBase != base || number < 2 ||
		name.Name != base+" #"+strconv.Itoa(number) || name.Name == name.Inherited {
		return fmt.Errorf("invalid clear name %s: %q is not a fresh numbered name for %q", sessionID, name.Name, base)
	}
	return nil
}

func clearNameParts(name string) (string, int) {
	index := strings.LastIndex(name, " #")
	if index <= 0 {
		return name, 0
	}
	number, err := strconv.Atoi(name[index+2:])
	if err != nil || number < 1 {
		// A nonnumeric suffix belongs to the user's base, not this convention.
		return name, 0
	}
	return name[:index], number
}

// Title overrides only Claude's inherited title, never a later deliberate rename.
func (name ClearName) Title(current string) string {
	if !name.Confirmed && (current == "" || current == name.Inherited) {
		return name.Name
	}
	return current
}

// ObserveTitle remembers when Claude has replaced the inherited name. Once
// observed, even a later deliberate rename back to the original must win.
func (name ClearName) ObserveTitle(current string) ClearName {
	if current != "" && current != name.Inherited {
		name.Confirmed = true
	}
	return name
}
