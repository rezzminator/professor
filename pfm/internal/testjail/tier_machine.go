//go:build jail || e2e || journey

package testjail

// unitBinary: this test binary was built with the jail tag (or the e2e or journey suite's), so its
// tests may touch the machine: it runs only where admitted holds, and re-runs in the fence elsewhere
// (fence.go).
const unitBinary = false
