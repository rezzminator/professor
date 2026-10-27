package call

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// DenyModal selects a modal choice through the target's backend-aware key door.
func DenyModal(ctx context.Context, target Target, down int, clk clock.Clock) error {
	if down < 0 {
		return fmt.Errorf("deny modal: negative down count %d", down)
	}
	engine, err := New(Dependencies{})
	if err != nil {
		return fmt.Errorf("deny modal transport: %w", err)
	}
	if clk == nil {
		clk = clock.Real
	}
	for i := 0; i < down; i++ {
		if err := engine.SendKey(ctx, target, "Down"); err != nil {
			return fmt.Errorf("deny modal Down: %w", err)
		}
		if err := clk.Sleep(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("deny modal wait: %w", err)
		}
	}
	if err := engine.SendKey(ctx, target, "Enter"); err != nil {
		return fmt.Errorf("deny modal Enter: %w", err)
	}
	return nil
}
