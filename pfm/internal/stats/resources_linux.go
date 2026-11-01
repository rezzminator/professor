//go:build linux

package stats

import "github.com/rezzminator/professor/pfm/internal/paths"

func readHostResources(root string, _ int64, _ int) (hostResources, error) {
	root = paths.ProcRootOr(root)
	return readLinuxHostResources(root)
}

func readDockerResources(root string) ([]Container, map[string]dockerSample, bool, []string, error) {
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	containers, raw, warnings, err := readDocker(root)
	return containers, raw, true, warnings, err
}
