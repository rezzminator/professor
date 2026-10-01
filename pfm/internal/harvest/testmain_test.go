package harvest

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	restore := StubPublicResolverForTest(RefusePublicLookupsForTest)
	code := m.Run()
	restore()
	os.Exit(code)
}
