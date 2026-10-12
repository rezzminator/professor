//go:build !linux && !darwin

package atomicfile

import "errors"

func swapPaths(a, b string) error {
	return errors.New("atomic exchange is not supported on this platform")
}
