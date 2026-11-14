//go:build linux

package stats

func readDockerResources(root string) ([]Container, map[string]dockerSample, bool, []string, error) {
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	containers, raw, warnings, err := readDocker(root)
	return containers, raw, true, warnings, err
}
