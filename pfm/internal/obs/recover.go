package obs

import "fmt"

// Recovered turns a recover() value into a bounded, scrubbed error a
// panicking handler's caller can hand back safely to ANY surface — a log
// record, an MCP tool result, a per-item fan-out slot — never a stack dump
// of the panicking call's own arguments: "panic in <label>: <type>:
// <message>", the message capped at MaxValueBytes and refused outright when
// it looks like a credential, the same rule Scrub applies to every other
// field. Call it from a deferred recover:
//
//	defer func() {
//		if recovered := recover(); recovered != nil {
//			err = obs.Recovered("tool "+name, recovered)
//		}
//	}()
func Recovered(label string, recovered any) error {
	message := TruncateValue(fmt.Sprint(recovered))
	if textHasSecret(message) {
		message = redactedValue
	}
	return fmt.Errorf("panic in %s: %T: %s", label, recovered, message)
}
