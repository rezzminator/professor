package paths

import "runtime"

// Platform is the OS and architecture this pfm binary runs on, as Go names
// them (darwin/linux, amd64/arm64): the OS door's one read of them for a
// caller that reports them rather than branching on them.
func Platform() (goos, goarch string) {
	return runtime.GOOS, runtime.GOARCH
}
