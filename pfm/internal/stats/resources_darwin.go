//go:build darwin

package stats

import "os"

func readDockerResources(root string) ([]Container, map[string]dockerSample, bool, []string, error) {
	// macOS has no cgroup filesystem. An existing override is still a fixture
	// and must exercise the same reader as Linux.
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			containers, raw, warnings, err := readDocker(root)
			return containers, raw, true, warnings, err
		}
	}
	return []Container{}, map[string]dockerSample{}, false, nil, nil
}
