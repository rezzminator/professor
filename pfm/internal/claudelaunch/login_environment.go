package claudelaunch

import "strings"

// loginEnvironment is the environment login(1) hands a fresh session, taken
// from environ: HOME, USER, LOGNAME, LANG, TMPDIR and every LC_* and
// XDG_* variable it sets, SHELL as shell (the one being probed, never the
// copied variable), and a system PATH. The probe runs in it so the PATH
// it prints is the login shell's own, never the one pfm was started with.
func loginEnvironment(environ []string, shell string) []string {
	env := []string{}
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "HOME", "USER", "LOGNAME", "LANG", "TMPDIR":
			env = append(env, entry)
		default:
			if strings.HasPrefix(name, "LC_") || strings.HasPrefix(name, "XDG_") {
				env = append(env, entry)
			}
		}
	}
	return append(env, envUserShell+"="+shell, envPATH+"="+loginBasePATH)
}

// loginHome is the HOME entry of env, "" when it has none or an empty one.
func loginHome(env []string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			return value
		}
	}
	return ""
}
