//go:build linux

package stats

func readHostResources(root string, _ int64, _ int) (hostResources, error) {
	if root == "" {
		root = "/proc"
	}
	return readLinuxHostResources(root)
}

func readDockerResources(root string) ([]Container, map[string]dockerSample, []string, error) {
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	return readDocker(root)
}
