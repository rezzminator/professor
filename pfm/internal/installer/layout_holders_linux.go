//go:build linux

package installer

func dbHolderPIDs(procRoot, db string) ([]string, error) {
	return procFDHolders(procRoot, db)
}
