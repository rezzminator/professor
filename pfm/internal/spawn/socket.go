package spawn

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"hostops/pfm/internal/engine"
)

// TestFreshSocketEnv fixes generated socket names in jailed tests.
const TestFreshSocketEnv = "PFM_TEST_FRESH_SOCKET"

// FreshSocket returns a unique socket name for a new engine session.
func FreshSocket(id engine.ID) string {
	if value := os.Getenv(TestFreshSocketEnv); value != "" {
		return value
	}
	descriptor := engine.MustLookup(id)
	var randomBytes [2]byte
	_, _ = rand.Read(randomBytes[:])
	return fmt.Sprintf(
		"%s%d-%d-%d",
		descriptor.SocketPrefix,
		time.Now().Unix(),
		os.Getpid(),
		binary.BigEndian.Uint16(randomBytes[:]),
	)
}
