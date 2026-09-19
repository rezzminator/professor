package paths

import (
	"errors"
	"testing"
)

func TestOSEnvGetAndLookupMirrorTheRealProcess(t *testing.T) {
	t.Setenv("PATHS_ENV_TEST_VAR", "value")
	env := OSEnv{}
	if got := env.Get("PATHS_ENV_TEST_VAR"); got != "value" {
		t.Fatalf("Get() = %q, want %q", got, "value")
	}
	value, ok := env.Lookup("PATHS_ENV_TEST_VAR")
	if !ok || value != "value" {
		t.Fatalf("Lookup() = (%q, %v), want (%q, true)", value, ok, "value")
	}
	if _, ok := env.Lookup("PATHS_ENV_TEST_VAR_DEFINITELY_UNSET"); ok {
		t.Fatal("Lookup() on an unset name reported ok=true")
	}
}

func TestMapEnvGetAndLookupReadOnlyValues(t *testing.T) {
	env := &MapEnv{Values: map[string]string{"NAME": "val"}}
	if got := env.Get("NAME"); got != "val" {
		t.Fatalf("Get() = %q, want %q", got, "val")
	}
	if got := env.Get("MISSING"); got != "" {
		t.Fatalf("Get() on a missing key = %q, want empty", got)
	}
	if _, ok := env.Lookup("MISSING"); ok {
		t.Fatal("Lookup() on a missing key reported ok=true")
	}

	var nilEnv MapEnv
	if got := nilEnv.Get("ANY"); got != "" {
		t.Fatalf("Get() on a zero-value MapEnv (nil Values) = %q, want empty", got)
	}
}

func TestMapEnvHomeHostnameUserReturnConfiguredErrors(t *testing.T) {
	wantErr := errors.New("boom")
	env := &MapEnv{HomeErr: wantErr, HostnameErr: wantErr, UserErr: wantErr}
	if _, err := env.Home(); !errors.Is(err, wantErr) {
		t.Fatalf("Home() error = %v, want %v", err, wantErr)
	}
	if _, err := env.Hostname(); !errors.Is(err, wantErr) {
		t.Fatalf("Hostname() error = %v, want %v", err, wantErr)
	}
	if _, err := env.User(); !errors.Is(err, wantErr) {
		t.Fatalf("User() error = %v, want %v", err, wantErr)
	}

	ok := &MapEnv{HomeDir: "/h", HostnameValue: "box", Username: "op"}
	if home, err := ok.Home(); err != nil || home != "/h" {
		t.Fatalf("Home() = (%q, %v), want (/h, nil)", home, err)
	}
	if hostname, err := ok.Hostname(); err != nil || hostname != "box" {
		t.Fatalf("Hostname() = (%q, %v), want (box, nil)", hostname, err)
	}
	if username, err := ok.User(); err != nil || username != "op" {
		t.Fatalf("User() = (%q, %v), want (op, nil)", username, err)
	}
}

func TestEnvOrFromAndEnvOrAgreeOverTheSameOverride(t *testing.T) {
	t.Setenv("PATHS_ENV_OR_TEST", "real")
	if got := EnvOr("PATHS_ENV_OR_TEST", "fallback"); got != "real" {
		t.Fatalf("EnvOr() = %q, want %q", got, "real")
	}
	if got := EnvOrFrom(OSEnv{}, "PATHS_ENV_OR_TEST", "fallback"); got != "real" {
		t.Fatalf("EnvOrFrom(OSEnv{}) = %q, want %q", got, "real")
	}

	mapped := &MapEnv{Values: map[string]string{"PATHS_ENV_OR_TEST": "mapped"}}
	if got := EnvOrFrom(mapped, "PATHS_ENV_OR_TEST", "fallback"); got != "mapped" {
		t.Fatalf("EnvOrFrom(MapEnv) = %q, want %q", got, "mapped")
	}
	if got := EnvOrFrom(mapped, "MISSING", "fallback"); got != "fallback" {
		t.Fatalf("EnvOrFrom(MapEnv) on a missing key = %q, want fallback %q", got, "fallback")
	}
}

func TestHomeFromRefusesInsideATestWithoutAJailOrRealHomeOptIn(t *testing.T) {
	env := &MapEnv{Values: map[string]string{}, HomeDir: "/would-be-real-home"}
	_, err := HomeFrom(env)
	if err == nil {
		t.Fatal("HomeFrom() with no PFM_HOME and no PFM_TEST_REAL_HOME returned nil error inside a test")
	}
}

func TestHomeFromReturnsTheJailOverrideWithoutTouchingEnvHome(t *testing.T) {
	env := &MapEnv{Values: map[string]string{EnvHome: "/jailed/home"}, HomeDir: "/would-be-real-home"}
	home, err := HomeFrom(env)
	if err != nil {
		t.Fatalf("HomeFrom() error = %v", err)
	}
	if home != "/jailed/home" {
		t.Fatalf("HomeFrom() = %q, want the jailed override %q", home, "/jailed/home")
	}
}

func TestHomeFromOptsIntoTheRealHomeProbeWithRealHomeSet(t *testing.T) {
	env := &MapEnv{
		Values:  map[string]string{EnvRealHome: "1"},
		HomeDir: "/real/operator/home",
	}
	home, err := HomeFrom(env)
	if err != nil {
		t.Fatalf("HomeFrom() error = %v", err)
	}
	if home != "/real/operator/home" {
		t.Fatalf("HomeFrom() = %q, want %q", home, "/real/operator/home")
	}
}

func TestHomeFromWrapsTheUnderlyingHomeError(t *testing.T) {
	wantErr := errors.New("no home for you")
	env := &MapEnv{
		Values:  map[string]string{EnvRealHome: "1"},
		HomeErr: wantErr,
	}
	_, err := HomeFrom(env)
	if !errors.Is(err, wantErr) {
		t.Fatalf("HomeFrom() error = %v, want it to wrap %v", err, wantErr)
	}
}

// TestHomeAndHomeFromOSEnvAgree proves Home() (the pre-existing function)
// and HomeFrom(OSEnv{}) (the seam it now delegates to) can never drift: the
// jailed PFM_HOME every other paths test relies on is read identically by
// both.
func TestHomeAndHomeFromOSEnvAgree(t *testing.T) {
	t.Setenv(EnvHome, t.TempDir())
	fromHome, errHome := Home()
	fromSeam, errSeam := HomeFrom(OSEnv{})
	if errHome != nil || errSeam != nil {
		t.Fatalf("Home() error = %v, HomeFrom(OSEnv{}) error = %v", errHome, errSeam)
	}
	if fromHome != fromSeam {
		t.Fatalf("Home() = %q but HomeFrom(OSEnv{}) = %q", fromHome, fromSeam)
	}
}

// TestConfigHomeFromUsesAbsoluteXDGOrHomeConfig pins the one rule
// internal/config's ResolvePath now composes over (L3-F9): an absolute
// XDG_CONFIG_HOME wins outright, and anything else — unset, blank,
// relative — falls back to home's own .config subdirectory.
func TestConfigHomeFromUsesAbsoluteXDGOrHomeConfig(t *testing.T) {
	home := "/jailed/home"
	cases := []struct {
		name string
		xdg  string
		want string
	}{
		{name: "absolute XDG wins", xdg: "/xdg/root", want: "/xdg/root"},
		{name: "relative XDG falls back to home", xdg: "relative", want: "/jailed/home/.config"},
		{name: "unset XDG falls back to home", xdg: "", want: "/jailed/home/.config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := &MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": tc.xdg}}
			if got := ConfigHomeFrom(env, home); got != tc.want {
				t.Fatalf("ConfigHomeFrom() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConfigHomeAndConfigHomeFromOSEnvAgree is ConfigHomeFrom's own version
// of TestHomeAndHomeFromOSEnvAgree: ConfigHome() and ConfigHomeFrom(OSEnv{})
// must read the same ambient XDG_CONFIG_HOME identically, or the two would
// silently drift the way HomeFrom's own seam was built to prevent.
func TestConfigHomeAndConfigHomeFromOSEnvAgree(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/an/xdg/root")
	home := "/jailed/home"
	if got, want := ConfigHome(home), ConfigHomeFrom(OSEnv{}, home); got != want {
		t.Fatalf("ConfigHome() = %q but ConfigHomeFrom(OSEnv{}) = %q", got, want)
	}
}
