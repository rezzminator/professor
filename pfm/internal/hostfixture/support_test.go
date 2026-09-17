package hostfixture

import (
	"os"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

func TestNewBaseJailsAFreshRootWithMatchingEnvAndFreshSeams(t *testing.T) {
	base := newBase(t)

	if base.Root == "" {
		t.Fatal("newBase().Root is empty")
	}
	if !strings.HasPrefix(base.Values.Home, base.Root) {
		t.Fatalf("Values.Home = %q, want it under Root %q", base.Values.Home, base.Root)
	}
	if got := base.Env.Get("HOME"); got != base.Values.Home {
		t.Fatalf("Env mirror of HOME = %q, want the jailed home %q", got, base.Values.Home)
	}
	if got := base.Env.Get(paths.EnvHome); got != base.Values.Home {
		t.Fatalf("Env mirror of PFM_HOME = %q, want the jailed home %q", got, base.Values.Home)
	}
	if len(base.Runner.Calls()) != 0 {
		t.Fatal("a fresh Base's Runner already has recorded calls")
	}
	if got := base.Clock.Now(); !got.Equal(epoch) {
		t.Fatalf("Clock.Now() = %v, want the fixed epoch %v", got, epoch)
	}
}

func TestSetEnvAndUnsetEnvMutateBothTheRealProcessAndTheMirror(t *testing.T) {
	base := newBase(t)

	setEnv(t, base, "HOSTFIXTURE_SUPPORT_TEST", "1")
	if got := os.Getenv("HOSTFIXTURE_SUPPORT_TEST"); got != "1" {
		t.Fatalf("real process env after setEnv = %q, want %q", got, "1")
	}
	if got := base.Env.Get("HOSTFIXTURE_SUPPORT_TEST"); got != "1" {
		t.Fatalf("Env mirror after setEnv = %q, want %q", got, "1")
	}

	unsetEnv(t, base, "HOSTFIXTURE_SUPPORT_TEST")
	if got := os.Getenv("HOSTFIXTURE_SUPPORT_TEST"); got != "" {
		t.Fatalf("real process env after unsetEnv = %q, want empty", got)
	}
	if _, ok := base.Env.Lookup("HOSTFIXTURE_SUPPORT_TEST"); ok {
		t.Fatal("Env mirror still holds the key after unsetEnv")
	}
}

func TestIsRootAgreesWithGeteuid(t *testing.T) {
	if got, want := isRoot(), os.Geteuid() == 0; got != want {
		t.Fatalf("isRoot() = %v, want %v (os.Geteuid()=%d)", got, want, os.Geteuid())
	}
}
