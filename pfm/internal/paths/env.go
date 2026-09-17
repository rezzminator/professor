package paths

import (
	"os"
	"os/user"
)

// Env abstracts a host's environment and identity reads — os.Getenv/
// LookupEnv/UserHomeDir/user.Current/os.Hostname — the seam every PFM_*
// override and host probe crosses instead of calling the os package
// directly (docs/dev/trains/testing-foundation/waves/3-unit-law/spec.md §
// Three seams item 3). OSEnv is the identity implementation EnvOr and Home
// already ran against before this existed; MapEnv is its in-memory twin for
// tests and hostfixture.
type Env interface {
	// Get returns name's value, or "" when unset — os.Getenv's contract.
	Get(name string) string
	// Lookup returns name's value and whether it was set at all —
	// os.LookupEnv's contract, for a caller that must tell "unset" apart
	// from "set to the empty string".
	Lookup(name string) (string, bool)
	// Home resolves the operator's home directory — os.UserHomeDir's
	// contract.
	Home() (string, error)
	// Hostname resolves the host's name — os.Hostname's contract.
	Hostname() (string, error)
	// User resolves the current OS username — user.Current's contract,
	// narrowed to the one field pfm ever reads from it.
	User() (string, error)
}

// OSEnv is Env over the real process: os.Getenv, os.LookupEnv,
// os.UserHomeDir, os.Hostname and user.Current.
type OSEnv struct{}

func (OSEnv) Get(name string) string            { return os.Getenv(name) }
func (OSEnv) Lookup(name string) (string, bool) { return os.LookupEnv(name) }
func (OSEnv) Home() (string, error)             { return os.UserHomeDir() }
func (OSEnv) Hostname() (string, error)         { return os.Hostname() }

func (OSEnv) User() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return current.Username, nil
}

// MapEnv is an in-memory Env for tests and hostfixture: Values seeds
// Get/Lookup; HomeDir/HomeErr, HostnameValue/HostnameErr and
// Username/UserErr seed the three identity probes, each answering its
// configured error when a fixture wants that probe to fail (hostfixture's
// NoHome leaves HomeDir "" with a HomeErr set).
type MapEnv struct {
	Values map[string]string

	HomeDir string
	HomeErr error

	HostnameValue string
	HostnameErr   error

	Username string
	UserErr  error
}

func (env *MapEnv) Get(name string) string {
	if env.Values == nil {
		return ""
	}
	return env.Values[name]
}

func (env *MapEnv) Lookup(name string) (string, bool) {
	if env.Values == nil {
		return "", false
	}
	value, ok := env.Values[name]
	return value, ok
}

func (env *MapEnv) Home() (string, error) {
	if env.HomeErr != nil {
		return "", env.HomeErr
	}
	return env.HomeDir, nil
}

func (env *MapEnv) Hostname() (string, error) {
	if env.HostnameErr != nil {
		return "", env.HostnameErr
	}
	return env.HostnameValue, nil
}

func (env *MapEnv) User() (string, error) {
	if env.UserErr != nil {
		return "", env.UserErr
	}
	return env.Username, nil
}
