package installer

// managedInstallArgs is the argv sequence, each run under sudo, that installs
// source at destination with mode 0644 and creates its parent: GNU install -D.
func managedInstallArgs(source, destination string) [][]string {
	return [][]string{{installProgram, "-D", "-m", "0644", source, destination}}
}
