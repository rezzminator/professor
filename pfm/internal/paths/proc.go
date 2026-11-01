package paths

// systemProcRoot is pfm's one literal process-table path: the kernel's proc
// filesystem, every process reader's root when none is configured.
const systemProcRoot = "/proc"

// SystemProcRoot is the kernel's proc filesystem root.
func SystemProcRoot() string { return systemProcRoot }

// ProcRootOr is root when a caller configured one (a jail's fixture tree),
// else the kernel's proc filesystem.
func ProcRootOr(root string) string {
	if root == "" {
		return systemProcRoot
	}
	return root
}
