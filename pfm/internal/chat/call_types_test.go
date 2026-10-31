package chat

import "testing"

// The call engine is the production SeatDoor the keys and capture verbs take.
var _ SeatDoor = (*CallEngine)(nil)

// TestNewCallEngineWithBuildsTheSeatDoor pins the zero-dependency engine the
// keys and capture doors send through: it builds without any configuration.
func TestNewCallEngineWithBuildsTheSeatDoor(t *testing.T) {
	engine, err := NewCallEngineWith(CallDependencies{})
	if err != nil || engine == nil {
		t.Fatalf("NewCallEngineWith(zero) = %v, %v; want an engine", engine, err)
	}
}
